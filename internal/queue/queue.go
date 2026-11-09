package queue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
)

type Job struct {
	ID         string         `json:"id"`
	Path       string         `json:"path"`
	Options    policy.Options `json:"options"`
	EnqueuedAt time.Time      `json:"enqueued_at"`
	FinishedAt time.Time      `json:"finished_at,omitempty"`
	Error      string         `json:"error,omitempty"`
	Output     *Output        `json:"output,omitempty"`
}

type Output struct {
	FinalDir       string `json:"final_dir"`
	VideoPath      string `json:"video_path"`
	TranscriptPath string `json:"transcript_path"`
	ManifestPath   string `json:"manifest_path"`
}

type Status struct {
	Status string `json:"status"`
	Job
}

var (
	ErrBusy      = errors.New("processor is busy; another process or queue worker holds the lock")
	ErrInvalidID = errors.New("job ID must be a non-empty file basename")
)

func Enqueue(cfg config.Config, inputPath string, options policy.Options) (Job, error) {
	if err := options.Validate(); err != nil {
		return Job{}, err
	}
	absPath, err := filepath.Abs(inputPath)
	if err != nil {
		return Job{}, fmt.Errorf("resolve recording path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return Job{}, fmt.Errorf("stat recording: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return Job{}, fmt.Errorf("recording must be a non-empty regular file")
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", absPath, info.ModTime().UnixNano(), info.Size())))
	id := fmt.Sprintf("%020d-%s", time.Now().UnixNano(), hex.EncodeToString(hash[:6]))
	job := Job{ID: id, Path: absPath, Options: options, EnqueuedAt: time.Now()}
	if err := publishQueuedJob(cfg, job); err != nil {
		return Job{}, err
	}
	return job, nil
}

func publishQueuedJob(cfg config.Config, job Job) error {
	if err := os.MkdirAll(cfg.QueueDir(), 0o700); err != nil {
		return fmt.Errorf("create queue directory: %w", err)
	}
	// launchd watches queue/ for any entries. Stage outside it so an interrupted
	// enqueue cannot wake the worker for a partial file.
	tempPath, err := stageJSON(cfg.StateDir, job)
	if err != nil {
		return fmt.Errorf("stage queued job: %w", err)
	}
	defer os.Remove(tempPath)
	if err := os.Link(tempPath, filepath.Join(cfg.QueueDir(), job.ID+".json")); err != nil {
		return fmt.Errorf("publish queued job: %w", err)
	}
	return nil
}

func Pending(cfg config.Config) ([]string, error) {
	entries, err := os.ReadDir(cfg.QueueDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read queue directory: %w", err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, filepath.Join(cfg.QueueDir(), entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func Read(path string) (Job, error) {
	job, err := readJob(path)
	if err != nil {
		return Job{}, err
	}
	if err := job.Options.Validate(); err != nil {
		return Job{}, fmt.Errorf("job options: %w", err)
	}
	return job, nil
}

func readJob(path string) (Job, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return Job{}, fmt.Errorf("read job: %w", err)
	}
	var job Job
	if err := json.Unmarshal(payload, &job); err != nil {
		return Job{}, fmt.Errorf("decode job: %w", err)
	}
	if strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.Path) == "" {
		return Job{}, fmt.Errorf("job is missing id or path")
	}
	return job, nil
}

func Lookup(cfg config.Config, id string) (Status, error) {
	if id == "" || id == "." || id == ".." || id != strings.TrimSpace(id) || strings.ContainsAny(id, "/\\\x00") {
		return Status{}, ErrInvalidID
	}
	// Read the queue first: Finish publishes the receipt before removing the
	// queue entry, so a transition cannot look like a missing job.
	queued, queueErr := readStatus(cfg.QueueDir(), id, "queued")
	terminal, err := terminalStatus(cfg, id)
	if !errors.Is(err, os.ErrNotExist) {
		return terminal, err
	}
	if errors.Is(queueErr, os.ErrNotExist) {
		return Status{}, fmt.Errorf("job %q not found: %w", id, os.ErrNotExist)
	}
	return queued, queueErr
}

func terminalStatus(cfg config.Config, id string) (Status, error) {
	done, err := readStatus(cfg.DoneDir(), id, "done")
	if !errors.Is(err, os.ErrNotExist) {
		return done, err
	}
	return readStatus(cfg.FailedDir(), id, "failed")
}

func readStatus(dir, id, state string) (Status, error) {
	// Failed receipts for rejected jobs can contain invalid processing options.
	job, err := readJob(filepath.Join(dir, id+".json"))
	if err != nil {
		return Status{}, fmt.Errorf("read %s receipt: %w", state, err)
	}
	if job.ID != id {
		return Status{}, fmt.Errorf("%s receipt for job %q contains ID %q", state, id, job.ID)
	}
	return Status{Status: state, Job: job}, nil
}

// RemoveFinishedPending discards a queue entry left behind after its receipt
// was published. The worker calls it while holding the processor lock.
func RemoveFinishedPending(cfg config.Config, queuePath string) (bool, error) {
	id := strings.TrimSuffix(filepath.Base(queuePath), ".json")
	if _, err := terminalStatus(cfg, id); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := os.Remove(queuePath); err != nil {
		return false, fmt.Errorf("remove finished job from queue: %w", err)
	}
	return true, nil
}

func Finish(cfg config.Config, queuePath string, job Job, runErr error) error {
	job.FinishedAt = time.Now()
	job.Error = ""
	destinationDir := cfg.DoneDir()
	if runErr != nil {
		destinationDir = cfg.FailedDir()
		job.Error = runErr.Error()
		job.Output = nil
	}
	if err := os.MkdirAll(destinationDir, 0o700); err != nil {
		return fmt.Errorf("create job destination: %w", err)
	}
	destination := filepath.Join(destinationDir, filepath.Base(queuePath))
	tempPath, err := stageJSON(destinationDir, job)
	if err != nil {
		return fmt.Errorf("stage job result: %w", err)
	}
	defer os.Remove(tempPath)
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("publish job result: %w", err)
	}
	if err := os.Remove(queuePath); err != nil {
		return fmt.Errorf("remove queued job: %w", err)
	}
	return nil
}

func WithLock(cfg config.Config, action func() error) error {
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	lockPath := filepath.Join(cfg.StateDir, "processor.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open queue lock: %w", err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrBusy
		}
		return fmt.Errorf("lock queue: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return action()
}

func stageJSON(dir string, value any) (string, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode job: %w", err)
	}
	payload = append(payload, '\n')
	file, err := os.CreateTemp(dir, ".job-*")
	if err != nil {
		return "", fmt.Errorf("create temporary job file: %w", err)
	}
	_, writeErr := file.Write(payload)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("write job: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("close job: %w", closeErr)
	}
	return file.Name(), nil
}
