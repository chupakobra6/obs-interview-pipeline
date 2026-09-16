package sobestech

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/media"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
	jobqueue "github.com/chupakobra6/obs-interview-pipeline/internal/queue"
)

func TestImporterPromptsForCompletedRecordingExactlyOnce(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	source, _ := writeCompletedRecording(t, cfg, "Техническое интервью_2026-09-07_14-02-08.682", "completed")
	probe := sourceTestProbe()
	importer := New(cfg)
	probeCalls := 0
	importer.Probe = func(context.Context, string, string) (media.Probe, error) {
		probeCalls++
		return probe, nil
	}
	importer.Now = func() time.Time { return time.Now().Add(time.Minute) }
	var prompts []string
	importer.Prompt = func(path string) error {
		prompts = append(prompts, path)
		return nil
	}

	report, err := importer.Import(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Prompted != 1 || report.Errors != 0 || len(report.Items) != 1 || report.Items[0].Status != "prompted" {
		t.Fatalf("first import report = %+v", report)
	}
	staging := filepath.Join(cfg.AllowedInputDir, "2026-09-07 14-02-08.mp4")
	if info, err := os.Stat(staging); err != nil || info.Size() != 1000 {
		t.Fatalf("staging copy = %v, %v", info, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("SobesTech source was removed: %v", err)
	}
	if len(prompts) != 1 || prompts[0] != staging {
		t.Fatalf("prompt calls = %v", prompts)
	}
	paths, err := jobqueue.Pending(cfg)
	if err != nil || len(paths) != 0 {
		t.Fatalf("importer unexpectedly queued jobs = %v, %v", paths, err)
	}

	before := snapshotReceipts(t, cfg)
	for range 100 {
		report, err = importer.Import(t.Context())
		if err != nil {
			t.Fatal(err)
		}
	}
	if report.Prompted != 0 || len(prompts) != 1 || probeCalls != 1 {
		t.Fatalf("duplicate work: report = %+v, prompts = %v, probes = %d", report, prompts, probeCalls)
	}
	assertUnchangedReceipts(t, cfg, before)

	// A changed input is checked again, but an existing job retains the user's decision.
	options := policy.Options{DeleteSource: false, AudioMode: policy.AudioMerge}
	job, err := jobqueue.Enqueue(cfg, staging, options)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(source, now, now); err != nil {
		t.Fatal(err)
	}
	report, err = importer.Import(t.Context())
	if err != nil || probeCalls != 2 || len(prompts) != 1 || len(report.Items) != 1 || report.Items[0].JobID != job.ID {
		t.Fatalf("changed input: report = %+v, probes = %d, prompts = %v, error = %v", report, probeCalls, prompts, err)
	}
	paths, _ = jobqueue.Pending(cfg)
	if len(paths) != 1 {
		t.Fatalf("duplicate jobs = %v", paths)
	}
	got, err := jobqueue.Read(paths[0])
	if err != nil || got.Options != options {
		t.Fatalf("user options changed: %+v, %v", got, err)
	}
}

func TestImporterTracksExistingValidatedResultWithoutReimport(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	source, _ := writeCompletedRecording(t, cfg, "Собеседование_2026-09-07_11-00-20.004", "completed")
	sourceProbe := sourceTestProbe()
	outputProbe := media.Probe{
		Streams: []media.Stream{
			{CodecType: "video", CodecName: "hevc", Width: 1512, Height: 982, AvgFrameRate: "30/1"},
			{CodecType: "audio", CodecName: "aac", BitRate: "96000"},
		},
		Format: media.Format{Duration: "60", Size: "500"},
	}
	staging := filepath.Join(cfg.AllowedInputDir, "2026-09-07 11-00-20.mp4")
	finalDir := filepath.Join(cfg.OutputDir, "2026-09-07 11-00-20")
	if err := os.MkdirAll(finalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(finalDir, "recording.mp4"), make([]byte, 500), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(finalDir, "transcript.md"), []byte("# transcript\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"source_path":  staging,
		"source_probe": sourceProbe,
		"output_probe": outputProbe,
		"options":      map[string]any{"delete_source": true, "audio_mode": "preserve"},
		"compression":  map[string]any{"output_audio_tracks": 1},
	}
	if err := writeJSONAtomic(filepath.Join(finalDir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	importer := New(cfg)
	importer.Now = func() time.Time { return time.Now().Add(time.Minute) }
	probeCalls := 0
	importer.Probe = func(_ context.Context, _ string, path string) (media.Probe, error) {
		probeCalls++
		if path == source {
			return sourceProbe, nil
		}
		return outputProbe, nil
	}
	report, err := importer.Import(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Prompted != 0 || len(report.Items) != 1 || report.Items[0].Status != "already-processed" {
		t.Fatalf("existing result report = %+v", report)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("existing result was staged again: %v", err)
	}
	if paths, _ := jobqueue.Pending(cfg); len(paths) != 0 {
		t.Fatalf("existing result was enqueued again: %v", paths)
	}
	before := snapshotReceipts(t, cfg)
	for range 10 {
		if _, err := importer.Import(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if probeCalls != 2 {
		t.Fatalf("unchanged published result was probed again: %d calls", probeCalls)
	}
	assertUnchangedReceipts(t, cfg, before)
	if err := os.Remove(filepath.Join(finalDir, "transcript.md")); err != nil {
		t.Fatal(err)
	}
	report, err = importer.Import(t.Context())
	if err == nil || report.Errors != 1 || probeCalls != 4 || report.Prompted != 0 {
		t.Fatalf("missing result was trusted: report = %+v, probes = %d, error = %v", report, probeCalls, err)
	}
	if err := os.Rename(finalDir, finalDir+"-moved"); err != nil {
		t.Fatal(err)
	}
	report, err = importer.Import(t.Context())
	if err == nil || report.Errors != 1 || probeCalls != 5 || report.Prompted != 0 {
		t.Fatalf("missing result directory was trusted: report = %+v, probes = %d, error = %v", report, probeCalls, err)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost published result must not stage a second user prompt: %v", err)
	}
}

func TestImporterWaitsForCompletedSettledManifest(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	_, manifestPath := writeCompletedRecording(t, cfg, "Интервью_2026-09-07_15-00-00.001", "recording")
	importer := New(cfg)
	importer.Now = time.Now
	report, err := importer.Import(t.Context())
	if err != nil || report.Prompted != 0 || report.Skipped != 1 {
		t.Fatalf("active recording report = %+v, %v", report, err)
	}
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["status"] = "completed"
	payload, _ = json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = importer.Import(t.Context())
	if err != nil || report.Prompted != 0 || report.Skipped != 1 {
		t.Fatalf("unsettled recording report = %+v, %v", report, err)
	}
}

func TestImporterBoundsRetriesForBadCompletedManifest(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	_, manifestPath := writeCompletedRecording(t, cfg, "Интервью_2026-09-07_16-00-00.001", "completed")
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest sourceManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Path = filepath.Join(cfg.SobesTechInputDir, "wrong.mp4")
	payload, _ = json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(manifestPath, past, past); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	importer := New(cfg)
	importer.Now = func() time.Time { return now }
	for attempt, advance := range []time.Duration{0, 31 * time.Second, 61 * time.Second} {
		now = now.Add(advance)
		report, importErr := importer.Import(t.Context())
		if importErr == nil || report.Errors != 1 {
			t.Fatalf("attempt %d report = %+v, error = %v", attempt+1, report, importErr)
		}
	}
	now = now.Add(time.Hour)
	report, err := importer.Import(t.Context())
	if err != nil || report.Errors != 0 || report.Skipped != 1 {
		t.Fatalf("terminal retry report = %+v, error = %v", report, err)
	}
}

func TestImporterReconcilesSuccessfulManualRetry(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	writeCompletedRecording(t, cfg, "Интервью_2026-09-07_17-00-00.001", "completed")
	importer := New(cfg)
	importer.Probe = func(context.Context, string, string) (media.Probe, error) { return sourceTestProbe(), nil }
	importer.Now = func() time.Time { return time.Now().Add(time.Minute) }
	importer.Prompt = func(string) error { return nil }
	if _, err := importer.Import(t.Context()); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(cfg.AllowedInputDir, "2026-09-07 17-00-00.mp4")
	first, err := jobqueue.Enqueue(cfg, staging, policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve})
	if err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(cfg.QueueDir(), first.ID+".json")
	if err := jobqueue.Finish(cfg, firstPath, first, errors.New("first attempt failed")); err != nil {
		t.Fatalf("finish first attempt: %v", err)
	}
	if _, err := importer.Import(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := jobqueue.Enqueue(cfg, first.Path, first.Options)
	if err != nil {
		t.Fatal(err)
	}
	secondPath := filepath.Join(cfg.QueueDir(), second.ID+".json")
	if err := jobqueue.Finish(cfg, secondPath, second, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Import(t.Context()); err != nil {
		t.Fatal(err)
	}
	receiptPaths, err := filepath.Glob(filepath.Join(cfg.SobesTechReceiptsDir(), "*.json"))
	if err != nil || len(receiptPaths) != 1 {
		t.Fatalf("receipt paths = %v, %v", receiptPaths, err)
	}
	saved, ok := readReceipt(receiptPaths[0])
	if !ok || saved.Status != "done" || saved.JobID != second.ID {
		t.Fatalf("retry receipt = %+v, valid = %t", saved, ok)
	}
	before := snapshotReceipts(t, cfg)
	if _, err := importer.Import(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertUnchangedReceipts(t, cfg, before)
}

func TestImporterRetriesRepairedVideoAndAdvancesIndependentRecording(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	broken, manifest := writeCompletedRecording(t, cfg, "Broken_2026-09-07_10-00-00.001", "completed")
	now := time.Now()
	importer := New(cfg)
	importer.Now = func() time.Time { return now }
	probeCalls, promptCalls := 0, 0
	importer.Probe = func(_ context.Context, _, path string) (media.Probe, error) {
		probeCalls++
		payload, err := os.ReadFile(path)
		if err != nil {
			return media.Probe{}, err
		}
		if path == broken && payload[0] == 0 {
			return media.Probe{}, errors.New("damaged MP4")
		}
		return sourceTestProbe(), nil
	}
	importer.Prompt = func(string) error { promptCalls++; return nil }
	for _, advance := range []time.Duration{0, 30 * time.Second, 60 * time.Second} {
		now = now.Add(advance)
		if report, err := importer.Import(t.Context()); err == nil || report.Errors != 1 {
			t.Fatalf("bad input should fail: %+v, %v", report, err)
		}
	}
	retryPath, _ := importer.retryReceiptPath(manifest)
	saved, ok := readReceipt(retryPath)
	if !ok || saved.Status != "failed" || saved.RetryCondition != "dependency-change" || saved.Error == "" {
		t.Fatalf("missing recovery condition: %+v", saved)
	}
	now = now.Add(time.Hour)
	writeCompletedRecording(t, cfg, "Independent_2026-09-07_11-00-00.001", "completed")
	if report, err := importer.Import(t.Context()); err != nil || report.Prompted != 1 || probeCalls != 4 {
		t.Fatalf("broken neighbour blocks new input: %+v, probes=%d, %v", report, probeCalls, err)
	}
	video := make([]byte, 1000)
	video[0] = 1
	if err := os.WriteFile(broken, video, 0o600); err != nil {
		t.Fatal(err)
	}
	repairedAt := now.Add(-time.Minute)
	if err := os.Chtimes(broken, repairedAt, repairedAt); err != nil {
		t.Fatal(err)
	}
	if report, err := importer.Import(t.Context()); err != nil || report.Prompted != 1 || probeCalls != 5 || promptCalls != 2 {
		t.Fatalf("repaired MP4 with unchanged manifest stays blocked: %+v, probes=%d, prompts=%d, %v", report, probeCalls, promptCalls, err)
	}
	if _, err := os.Stat(retryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered error retained: %v", err)
	}
	if report, err := importer.Import(t.Context()); err != nil || report.Prompted != 0 || probeCalls != 5 || promptCalls != 2 {
		t.Fatalf("recovered import duplicated work: %+v, probes=%d, prompts=%d, %v", report, probeCalls, promptCalls, err)
	}
}

func TestImporterLimitsTemporaryToolRetriesAndRecoversOnTime(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	_, manifest := writeCompletedRecording(t, cfg, "Interview_2026-09-07_12-00-00.001", "completed")
	now := time.Now()
	importer := New(cfg)
	importer.Now = func() time.Time { return now }
	probeCalls, promptCalls := 0, 0
	available := false
	importer.Probe = func(context.Context, string, string) (media.Probe, error) {
		probeCalls++
		if !available {
			return media.Probe{}, &exec.Error{Name: "ffprobe", Err: exec.ErrNotFound}
		}
		return sourceTestProbe(), nil
	}
	importer.Prompt = func(string) error { promptCalls++; return nil }
	for _, advance := range []time.Duration{0, 30 * time.Second, 60 * time.Second} {
		now = now.Add(advance)
		if _, err := importer.Import(t.Context()); err == nil {
			t.Fatal("unavailable probe must fail")
		}
	}
	retryPath, _ := importer.retryReceiptPath(manifest)
	saved, ok := readReceipt(retryPath)
	if !ok || saved.Status != "retrying" || saved.RetryCondition != "dependency-change-or-time" || !saved.NextAttemptAt.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("temporary failure cannot recover: %+v", saved)
	}
	for range 29 {
		now = now.Add(30 * time.Second)
		if report, err := importer.Import(t.Context()); err != nil || report.Skipped != 1 || probeCalls != 3 {
			t.Fatalf("retry storm: %+v, probes=%d, %v", report, probeCalls, err)
		}
	}
	available = true
	now = now.Add(30 * time.Second)
	if report, err := importer.Import(t.Context()); err != nil || report.Prompted != 1 || probeCalls != 4 || promptCalls != 1 {
		t.Fatalf("timed recovery failed: %+v, probes=%d, %v", report, probeCalls, err)
	}
}

func TestImporterRetriesWhenProbeExecutableAppears(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	cfg.FFprobeCommand = filepath.Join(t.TempDir(), "ffprobe")
	writeCompletedRecording(t, cfg, "Interview_2026-09-07_13-00-00.001", "completed")
	importer := New(cfg)
	importer.Now = time.Now
	probeCalls := 0
	importer.Probe = func(context.Context, string, string) (media.Probe, error) {
		probeCalls++
		if _, err := os.Stat(cfg.FFprobeCommand); err != nil {
			return media.Probe{}, err
		}
		return sourceTestProbe(), nil
	}
	importer.Prompt = func(string) error { return nil }
	if _, err := importer.Import(t.Context()); err == nil {
		t.Fatal("missing probe must fail")
	}
	if err := os.WriteFile(cfg.FFprobeCommand, []byte("test executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if report, err := importer.Import(t.Context()); err != nil || report.Prompted != 1 || probeCalls != 2 {
		t.Fatalf("restored dependency must bypass backoff: %+v, probes=%d, %v", report, probeCalls, err)
	}
}

func TestImporterPromptFailureKeepsBackoffAfterStagingAndRecovers(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	writeCompletedRecording(t, cfg, "Interview_2026-09-07_13-30-00.001", "completed")
	now := time.Now()
	importer := New(cfg)
	importer.Now = func() time.Time { return now }
	probeCalls, promptCalls := 0, 0
	importer.Probe = func(context.Context, string, string) (media.Probe, error) {
		probeCalls++
		return sourceTestProbe(), nil
	}
	importer.Prompt = func(string) error {
		promptCalls++
		if promptCalls <= 3 {
			return errors.New("LaunchServices temporarily unavailable")
		}
		return nil
	}
	for _, advance := range []time.Duration{0, 30 * time.Second, 60 * time.Second} {
		now = now.Add(advance)
		if _, err := importer.Import(t.Context()); err == nil {
			t.Fatal("prompt launch must fail")
		}
		calls := promptCalls
		if report, err := importer.Import(t.Context()); err != nil || report.Skipped != 1 || promptCalls != calls || probeCalls != calls {
			t.Fatalf("created staging copy bypassed backoff: %+v, prompts=%d, probes=%d, %v", report, promptCalls, probeCalls, err)
		}
	}
	now = now.Add(15 * time.Minute)
	if report, err := importer.Import(t.Context()); err != nil || report.Prompted != 1 || promptCalls != 4 {
		t.Fatalf("prompt launcher did not recover: %+v, prompts=%d, %v", report, promptCalls, err)
	}
	if report, err := importer.Import(t.Context()); err != nil || report.Skipped != 1 || promptCalls != 4 || probeCalls != 4 {
		t.Fatalf("recovered prompt launched twice: %+v, prompts=%d, probes=%d, %v", report, promptCalls, probeCalls, err)
	}
}

func TestImporterPreservesUserDecisionAcrossConfigChanges(t *testing.T) {
	for _, status := range []string{"prompted", "queued", "done", "failed"} {
		t.Run(status, func(t *testing.T) {
			cfg := importerTestConfig(t.TempDir())
			source, manifest := writeCompletedRecording(t, cfg, "Interview_2026-09-07_14-00-00.001", "completed")
			importer := New(cfg)
			importer.Probe = func(context.Context, string, string) (media.Probe, error) { return sourceTestProbe(), nil }
			importer.Prompt = func(string) error { return nil }
			if _, err := importer.Import(t.Context()); err != nil {
				t.Fatal(err)
			}
			staging, _ := importer.stagingPath(source)
			options := policy.Options{DeleteSource: false, AudioMode: policy.AudioMerge}
			if status != "prompted" {
				job, err := jobqueue.Enqueue(cfg, staging, options)
				if err != nil {
					t.Fatal(err)
				}
				if status == "done" || status == "failed" {
					var result error
					if status == "failed" {
						result = errors.New("ASR needs retry")
					}
					if err := jobqueue.Finish(cfg, filepath.Join(cfg.QueueDir(), job.ID+".json"), job, result); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := importer.Import(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				// Skipping the native prompt removes its disposable copy, but keeps the decision.
				if err := os.Remove(staging); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotReceipts(t, cfg)
			cfg.FFprobeCommand = filepath.Join(t.TempDir(), "updated-ffprobe")
			cfg.OutputWidth, cfg.OutputFPS = 1920, 60
			cfg.DeleteSourceOnSuccess = !cfg.DeleteSourceOnSuccess
			restarted := New(cfg)
			restarted.Probe = func(context.Context, string, string) (media.Probe, error) {
				t.Fatal("existing user decision triggered a technical recheck")
				return media.Probe{}, nil
			}
			restarted.Prompt = func(string) error { t.Fatal("duplicate user prompt"); return nil }
			for range 3 {
				report, err := restarted.Import(t.Context())
				if err != nil || report.Prompted != 0 || report.Skipped != 1 {
					t.Fatalf("decision lost after config change: %+v, %v", report, err)
				}
			}
			assertUnchangedReceipts(t, cfg, before)
			if status != "prompted" {
				job, gotStatus, found := findJob(cfg, staging)
				if !found || gotStatus != status || job.Options != options {
					t.Fatalf("job or user parameters changed: %+v, %s", job, gotStatus)
				}
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatalf("original removed: %v", err)
			}
			if _, err := os.Stat(manifest); err != nil {
				t.Fatalf("source manifest removed: %v", err)
			}
		})
	}
}

type receiptSnapshot struct {
	contents string
	modified time.Time
}

func snapshotReceipts(t *testing.T, cfg config.Config) map[string]receiptSnapshot {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(cfg.SobesTechReceiptsDir(), "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]receiptSnapshot)
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		result[path] = receiptSnapshot{contents: string(contents), modified: info.ModTime()}
	}
	return result
}

func assertUnchangedReceipts(t *testing.T, cfg config.Config, before map[string]receiptSnapshot) {
	t.Helper()
	after := snapshotReceipts(t, cfg)
	if len(before) != len(after) {
		t.Fatalf("receipt count changed: %d -> %d", len(before), len(after))
	}
	for path, expected := range before {
		if after[path] != expected {
			t.Errorf("unchanged import rewrote receipt %s", path)
		}
	}
}

func importerTestConfig(dir string) config.Config {
	cfg := config.Default(dir)
	cfg.AllowedInputDir = filepath.Join(dir, "Movies")
	cfg.OutputDir = filepath.Join(cfg.AllowedInputDir, "Interviews")
	cfg.StateDir = filepath.Join(dir, "state")
	cfg.SobesTechInputDir = filepath.Join(dir, "SobesTech")
	cfg.SobesTechSettleSeconds = 15
	return cfg
}

func writeCompletedRecording(t *testing.T, cfg config.Config, stem, status string) (string, string) {
	t.Helper()
	dir := filepath.Join(cfg.SobesTechInputDir, "Meeting")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(dir, stem+".mp4")
	manifestPath := filepath.Join(dir, stem+".json")
	if err := os.WriteFile(videoPath, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := sourceManifest{ID: "recording-id", Status: status, Path: videoPath, ManifestPath: manifestPath, SizeBytes: 1000, DurationMS: 60000, Error: json.RawMessage("null")}
	payload, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(videoPath, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(manifestPath, past, past); err != nil {
		t.Fatal(err)
	}
	return videoPath, manifestPath
}

func sourceTestProbe() media.Probe {
	return media.Probe{
		Streams: []media.Stream{
			{CodecType: "video", CodecName: "h264", Width: 3024, Height: 1964, AvgFrameRate: "30/1"},
			{CodecType: "audio", CodecName: "aac", BitRate: "160000"},
		},
		Format: media.Format{Duration: "60", Size: "1000"},
	}
}
