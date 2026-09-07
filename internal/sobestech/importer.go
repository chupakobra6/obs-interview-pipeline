package sobestech

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/media"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
	jobqueue "github.com/chupakobra6/obs-interview-pipeline/internal/queue"
)

const (
	receiptVersion        = 1
	maximumImportAttempts = 3
)

var recordingTimestampPattern = regexp.MustCompile(`_([0-9]{4}-[0-9]{2}-[0-9]{2})_([0-9]{2}-[0-9]{2}-[0-9]{2})\.[0-9]+$`)

type ProbeFunc func(context.Context, string, string) (media.Probe, error)
type EnqueueFunc func(config.Config, string, policy.Options) (jobqueue.Job, error)

type Importer struct {
	Config  config.Config
	Probe   ProbeFunc
	Enqueue EnqueueFunc
	Now     func() time.Time
}

type Report struct {
	Scanned  int    `json:"scanned"`
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped"`
	Errors   int    `json:"errors"`
	Items    []Item `json:"items,omitempty"`
}

type Item struct {
	SourcePath  string `json:"source_path"`
	StagingPath string `json:"staging_path,omitempty"`
	JobID       string `json:"job_id,omitempty"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

type sourceManifest struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Status       string          `json:"status"`
	Path         string          `json:"path"`
	ManifestPath string          `json:"manifestPath"`
	SizeBytes    int64           `json:"sizeBytes"`
	DurationMS   int64           `json:"durationMs"`
	Error        json.RawMessage `json:"error"`
}

type receipt struct {
	Version              int       `json:"version"`
	SourceID             string    `json:"source_id"`
	SourcePath           string    `json:"source_path"`
	ManifestPath         string    `json:"manifest_path"`
	SourceSizeBytes      int64     `json:"source_size_bytes"`
	SourceModifiedAtNano int64     `json:"source_modified_at_unix_nano"`
	StagingPath          string    `json:"staging_path"`
	SourceSHA256         string    `json:"source_sha256,omitempty"`
	JobID                string    `json:"job_id,omitempty"`
	Status               string    `json:"status"`
	UpdatedAt            time.Time `json:"updated_at"`
	Error                string    `json:"error,omitempty"`
	Attempts             int       `json:"attempts,omitempty"`
	NextAttemptAt        time.Time `json:"next_attempt_at,omitempty"`
}

type finalManifest struct {
	SourcePath  string         `json:"source_path"`
	SourceProbe media.Probe    `json:"source_probe"`
	OutputProbe media.Probe    `json:"output_probe"`
	Options     policy.Options `json:"options"`
	Compression struct {
		OutputAudioTracks int `json:"output_audio_tracks"`
	} `json:"compression"`
}

func New(cfg config.Config) Importer {
	return Importer{
		Config:  cfg,
		Probe:   media.Inspect,
		Enqueue: jobqueue.Enqueue,
		Now:     time.Now,
	}
}

func (i Importer) Import(ctx context.Context) (Report, error) {
	var report Report
	if _, err := os.Stat(i.Config.SobesTechInputDir); errors.Is(err, os.ErrNotExist) {
		return report, nil
	} else if err != nil {
		return report, fmt.Errorf("inspect SobesTech recordings root: %w", err)
	}
	if err := os.MkdirAll(i.Config.SobesTechReceiptsDir(), 0o700); err != nil {
		return report, fmt.Errorf("create SobesTech receipt directory: %w", err)
	}
	lock, acquired, err := acquireLock(filepath.Join(i.Config.StateDir, "sobestech-importer.lock"))
	if err != nil {
		return report, err
	}
	if !acquired {
		return report, nil
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()

	manifestPaths, err := discoverManifests(i.Config.SobesTechInputDir)
	if err != nil {
		return report, err
	}
	var itemErrors []error
	for _, manifestPath := range manifestPaths {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(err, errors.Join(itemErrors...))
		}
		report.Scanned++
		retryPath, retryPathErr := i.retryReceiptPath(manifestPath)
		if retryPathErr != nil {
			report.Errors++
			itemErrors = append(itemErrors, retryPathErr)
			continue
		}
		if saved, ok := readReceipt(retryPath); ok && (saved.Status == "failed" || (!saved.NextAttemptAt.IsZero() && i.Now().Before(saved.NextAttemptAt))) {
			report.Skipped++
			continue
		}
		item, action, itemErr := i.importManifest(ctx, manifestPath)
		if itemErr != nil {
			saved, _ := readReceipt(retryPath)
			saved.Version = receiptVersion
			saved.ManifestPath = manifestPath
			saved.Attempts++
			saved.Status = "retrying"
			saved.Error = itemErr.Error()
			saved.UpdatedAt = i.Now()
			if saved.Attempts >= maximumImportAttempts {
				saved.Status = "failed"
				saved.NextAttemptAt = time.Time{}
			} else {
				saved.NextAttemptAt = i.Now().Add(time.Duration(1<<(saved.Attempts-1)) * 30 * time.Second)
			}
			if writeErr := writeJSONAtomic(retryPath, saved); writeErr != nil {
				itemErr = errors.Join(itemErr, writeErr)
			}
			report.Errors++
			item.Status = saved.Status
			item.Error = itemErr.Error()
			report.Items = append(report.Items, item)
			itemErrors = append(itemErrors, fmt.Errorf("import %s: %w", manifestPath, itemErr))
			continue
		}
		switch action {
		case "queued":
			report.Imported++
			report.Items = append(report.Items, item)
		case "tracked", "already-processed":
			report.Items = append(report.Items, item)
		default:
			report.Skipped++
		}
	}
	return report, errors.Join(itemErrors...)
}

func (i Importer) importManifest(ctx context.Context, manifestPath string) (Item, string, error) {
	item := Item{SourcePath: strings.TrimSuffix(manifestPath, filepath.Ext(manifestPath)) + ".mp4"}
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		return item, "", fmt.Errorf("read completion manifest: %w", err)
	}
	var manifest sourceManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return item, "", fmt.Errorf("decode completion manifest: %w", err)
	}
	if manifest.Status != "completed" {
		return item, "skipped", nil
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return item, "", fmt.Errorf("completed manifest has no recording id")
	}
	if raw := strings.TrimSpace(string(manifest.Error)); raw != "" && raw != "null" && raw != `""` {
		return item, "", fmt.Errorf("completed manifest contains recording error")
	}
	expectedVideo := strings.TrimSuffix(manifestPath, filepath.Ext(manifestPath)) + ".mp4"
	expectedVideo, err = filepath.Abs(expectedVideo)
	if err != nil {
		return item, "", fmt.Errorf("resolve SobesTech video path: %w", err)
	}
	item.SourcePath = expectedVideo
	if filepath.Clean(manifest.Path) != expectedVideo {
		return item, "", fmt.Errorf("manifest video path %q differs from sibling MP4 %q", manifest.Path, expectedVideo)
	}
	videoInfo, err := os.Stat(expectedVideo)
	if err != nil {
		return item, "", fmt.Errorf("inspect completed SobesTech video: %w", err)
	}
	manifestInfo, err := os.Stat(manifestPath)
	if err != nil {
		return item, "", fmt.Errorf("inspect completion manifest: %w", err)
	}
	if !videoInfo.Mode().IsRegular() || videoInfo.Size() <= 0 {
		return item, "", fmt.Errorf("completed SobesTech video is not a non-empty regular file")
	}
	if manifest.SizeBytes > 0 && manifest.SizeBytes != videoInfo.Size() {
		return item, "", fmt.Errorf("manifest size %d differs from video size %d", manifest.SizeBytes, videoInfo.Size())
	}
	latestChange := videoInfo.ModTime()
	if manifestInfo.ModTime().After(latestChange) {
		latestChange = manifestInfo.ModTime()
	}
	if i.Now().Sub(latestChange) < time.Duration(i.Config.SobesTechSettleSeconds)*time.Second {
		return item, "skipped", nil
	}

	sourceProbe, err := i.Probe(ctx, i.Config.FFprobeCommand, expectedVideo)
	if err != nil {
		return item, "", fmt.Errorf("probe completed SobesTech video: %w", err)
	}
	if _, ok := sourceProbe.Video(); !ok || sourceProbe.AudioCount() == 0 || sourceProbe.DurationSeconds() <= 0 {
		return item, "", fmt.Errorf("completed SobesTech video has invalid media streams")
	}
	stagingPath, err := i.stagingPath(expectedVideo)
	if err != nil {
		return item, "", err
	}
	item.StagingPath = stagingPath
	receiptPath := i.receiptPath(manifest, expectedVideo, videoInfo)
	if saved, ok := readReceipt(receiptPath); ok {
		item.JobID = saved.JobID
		item.Status = saved.Status
		if saved.Status == "queued" || saved.Status == "done" || saved.Status == "failed" {
			if job, status, found := findJob(i.Config, stagingPath); found {
				saved.JobID = job.ID
				saved.Status = status
				saved.Error = job.Error
				saved.UpdatedAt = i.Now()
				_ = writeJSONAtomic(receiptPath, saved)
				item.JobID = job.ID
				item.Status = status
			}
		}
		switch item.Status {
		case "queued", "done", "failed", "already-processed":
			return item, "skipped", nil
		}
	}

	if valid, err := i.validExistingFinal(ctx, stagingPath, sourceProbe); err != nil {
		return item, "", err
	} else if valid {
		item.Status = "already-processed"
		saved := newReceipt(manifest, manifestPath, expectedVideo, videoInfo, stagingPath, item.Status, i.Now())
		if err := writeJSONAtomic(receiptPath, saved); err != nil {
			return item, "", err
		}
		return item, "already-processed", nil
	}
	if job, status, found := findJob(i.Config, stagingPath); found {
		item.JobID = job.ID
		item.Status = status
		saved := newReceipt(manifest, manifestPath, expectedVideo, videoInfo, stagingPath, status, i.Now())
		saved.JobID = job.ID
		saved.Error = job.Error
		if err := writeJSONAtomic(receiptPath, saved); err != nil {
			return item, "", err
		}
		return item, "tracked", nil
	}

	sourceHash, err := ensureStagingCopy(expectedVideo, stagingPath, videoInfo)
	if err != nil {
		return item, "", err
	}
	saved := newReceipt(manifest, manifestPath, expectedVideo, videoInfo, stagingPath, "staged", i.Now())
	saved.SourceSHA256 = sourceHash
	if err := writeJSONAtomic(receiptPath, saved); err != nil {
		return item, "", err
	}
	job, err := i.Enqueue(i.Config, stagingPath, policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve})
	if err != nil {
		return item, "", fmt.Errorf("enqueue imported SobesTech video: %w", err)
	}
	saved.JobID = job.ID
	saved.Status = "queued"
	saved.UpdatedAt = i.Now()
	if err := writeJSONAtomic(receiptPath, saved); err != nil {
		return item, "", err
	}
	item.JobID = job.ID
	item.Status = "queued"
	return item, "queued", nil
}

func (i Importer) stagingPath(sourcePath string) (string, error) {
	stem := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
	match := recordingTimestampPattern.FindStringSubmatch(stem)
	if len(match) != 3 {
		return "", fmt.Errorf("SobesTech filename has no supported completion timestamp: %s", filepath.Base(sourcePath))
	}
	return filepath.Join(i.Config.AllowedInputDir, match[1]+" "+match[2]+".mp4"), nil
}

func (i Importer) receiptPath(manifest sourceManifest, sourcePath string, info os.FileInfo) string {
	identity := fmt.Sprintf("%s\x00%s\x00%d\x00%d", manifest.ID, sourcePath, info.Size(), info.ModTime().UnixNano())
	digest := sha256.Sum256([]byte(identity))
	return filepath.Join(i.Config.SobesTechReceiptsDir(), fmt.Sprintf("%x.json", digest[:12]))
}

func (i Importer) retryReceiptPath(manifestPath string) (string, error) {
	info, err := os.Stat(manifestPath)
	if err != nil {
		return "", fmt.Errorf("inspect SobesTech manifest for retry state: %w", err)
	}
	identity := fmt.Sprintf("%s\x00%d\x00%d", manifestPath, info.Size(), info.ModTime().UnixNano())
	digest := sha256.Sum256([]byte(identity))
	return filepath.Join(i.Config.SobesTechReceiptsDir(), fmt.Sprintf("retry-%x.json", digest[:12])), nil
}

func (i Importer) validExistingFinal(ctx context.Context, stagingPath string, sourceProbe media.Probe) (bool, error) {
	stem := strings.TrimSuffix(filepath.Base(stagingPath), filepath.Ext(stagingPath))
	finalDir := filepath.Join(i.Config.OutputDir, stem)
	if _, err := os.Stat(finalDir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect existing pipeline result: %w", err)
	}
	payload, err := os.ReadFile(filepath.Join(finalDir, "manifest.json"))
	if err != nil {
		return false, fmt.Errorf("existing pipeline result has no readable manifest: %w", err)
	}
	var saved finalManifest
	if err := json.Unmarshal(payload, &saved); err != nil {
		return false, fmt.Errorf("decode existing pipeline manifest: %w", err)
	}
	if filepath.Clean(saved.SourcePath) != filepath.Clean(stagingPath) || !reflect.DeepEqual(saved.SourceProbe, sourceProbe) {
		return false, fmt.Errorf("existing pipeline result belongs to a different SobesTech source")
	}
	expectedOptions := policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve}
	if saved.Options != expectedOptions {
		return false, fmt.Errorf("existing pipeline result uses different processing options")
	}
	actualOutput, err := i.Probe(ctx, i.Config.FFprobeCommand, filepath.Join(finalDir, "recording.mp4"))
	if err != nil {
		return false, fmt.Errorf("probe existing pipeline video: %w", err)
	}
	if !reflect.DeepEqual(actualOutput, saved.OutputProbe) {
		return false, fmt.Errorf("existing pipeline video differs from its manifest")
	}
	if err := media.ValidateCompressed(sourceProbe, actualOutput, i.Config.OutputWidth, i.Config.OutputHeight, i.Config.OutputFPS, i.Config.AudioBitrateKbps, saved.Compression.OutputAudioTracks); err != nil {
		return false, fmt.Errorf("validate existing pipeline video: %w", err)
	}
	if err := media.ValidateSizeLimit(sourceProbe, actualOutput, i.Config.MaxOutputSizePercent); err != nil {
		return false, fmt.Errorf("validate existing pipeline video: %w", err)
	}
	transcriptInfo, err := os.Stat(filepath.Join(finalDir, "transcript.md"))
	if err != nil || !transcriptInfo.Mode().IsRegular() || transcriptInfo.Size() == 0 {
		return false, fmt.Errorf("existing pipeline transcript is unavailable")
	}
	return true, nil
}

func discoverManifests(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan SobesTech recordings: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func findJob(cfg config.Config, stagingPath string) (jobqueue.Job, string, bool) {
	for _, location := range []struct {
		dir    string
		status string
	}{
		{dir: cfg.QueueDir(), status: "queued"},
		{dir: cfg.DoneDir(), status: "done"},
		{dir: cfg.FailedDir(), status: "failed"},
	} {
		entries, err := os.ReadDir(location.dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			job, err := jobqueue.Read(filepath.Join(location.dir, entry.Name()))
			if err == nil && filepath.Clean(job.Path) == filepath.Clean(stagingPath) {
				return job, location.status, true
			}
		}
	}
	return jobqueue.Job{}, "", false
}

func ensureStagingCopy(sourcePath, stagingPath string, expected os.FileInfo) (string, error) {
	if info, err := os.Stat(stagingPath); err == nil {
		if !info.Mode().IsRegular() || info.Size() != expected.Size() {
			return "", fmt.Errorf("SobesTech staging collision at %s", stagingPath)
		}
		sourceHash, err := hashFile(sourcePath)
		if err != nil {
			return "", err
		}
		stagingHash, err := hashFile(stagingPath)
		if err != nil {
			return "", err
		}
		if sourceHash != stagingHash {
			return "", fmt.Errorf("SobesTech staging content differs at %s", stagingPath)
		}
		return sourceHash, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect SobesTech staging path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(stagingPath), 0o700); err != nil {
		return "", fmt.Errorf("create SobesTech staging directory: %w", err)
	}
	input, err := os.Open(sourcePath)
	if err != nil {
		return "", fmt.Errorf("open SobesTech video: %w", err)
	}
	defer input.Close()
	temp, err := os.CreateTemp(filepath.Dir(stagingPath), ".sobestech-import-*.mp4")
	if err != nil {
		return "", fmt.Errorf("create SobesTech staging file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("secure SobesTech staging file: %w", err)
	}
	hasher := sha256.New()
	copied, copyErr := io.Copy(io.MultiWriter(temp, hasher), input)
	if copyErr == nil {
		copyErr = temp.Sync()
	}
	closeErr := temp.Close()
	if copyErr != nil {
		return "", fmt.Errorf("copy SobesTech video: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close SobesTech staging file: %w", closeErr)
	}
	current, err := os.Stat(sourcePath)
	if err != nil || current.Size() != expected.Size() || !current.ModTime().Equal(expected.ModTime()) || copied != expected.Size() {
		return "", fmt.Errorf("SobesTech video changed during import")
	}
	if err := os.Rename(tempPath, stagingPath); err != nil {
		return "", fmt.Errorf("publish SobesTech staging file: %w", err)
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s for hashing: %w", path, err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func newReceipt(manifest sourceManifest, manifestPath, sourcePath string, info os.FileInfo, stagingPath, status string, now time.Time) receipt {
	return receipt{
		Version:              receiptVersion,
		SourceID:             manifest.ID,
		SourcePath:           sourcePath,
		ManifestPath:         manifestPath,
		SourceSizeBytes:      info.Size(),
		SourceModifiedAtNano: info.ModTime().UnixNano(),
		StagingPath:          stagingPath,
		Status:               status,
		UpdatedAt:            now,
	}
}

func readReceipt(path string) (receipt, bool) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return receipt{}, false
	}
	var saved receipt
	if json.Unmarshal(payload, &saved) != nil || saved.Version != receiptVersion {
		return receipt{}, false
	}
	return saved, true
}

func writeJSONAtomic(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode SobesTech receipt: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create SobesTech receipt parent: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".receipt-*.json")
	if err != nil {
		return fmt.Errorf("create SobesTech receipt: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err == nil {
		_, err = temp.Write(payload)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return fmt.Errorf("write SobesTech receipt: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close SobesTech receipt: %w", closeErr)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish SobesTech receipt: %w", err)
	}
	return nil
}

func acquireLock(path string) (*os.File, bool, error) {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open SobesTech importer lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lock SobesTech importer: %w", err)
	}
	return lock, true, nil
}
