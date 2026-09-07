package sobestech

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/media"
	jobqueue "github.com/chupakobra6/obs-interview-pipeline/internal/queue"
)

func TestImporterEnqueuesCompletedRecordingExactlyOnce(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	source, _ := writeCompletedRecording(t, cfg, "Техническое интервью_2026-09-07_14-02-08.682", "completed")
	probe := sourceTestProbe()
	importer := New(cfg)
	importer.Probe = func(context.Context, string, string) (media.Probe, error) { return probe, nil }
	importer.Now = func() time.Time { return time.Now().Add(time.Minute) }

	report, err := importer.Import(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Imported != 1 || report.Errors != 0 || len(report.Items) != 1 || report.Items[0].Status != "queued" {
		t.Fatalf("first import report = %+v", report)
	}
	staging := filepath.Join(cfg.AllowedInputDir, "2026-09-07 14-02-08.mp4")
	if info, err := os.Stat(staging); err != nil || info.Size() != 1000 {
		t.Fatalf("staging copy = %v, %v", info, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("SobesTech source was removed: %v", err)
	}
	paths, err := jobqueue.Pending(cfg)
	if err != nil || len(paths) != 1 {
		t.Fatalf("pending jobs = %v, %v", paths, err)
	}
	job, err := jobqueue.Read(paths[0])
	if err != nil || job.Path != staging || !job.Options.DeleteSource {
		t.Fatalf("queued job = %+v, %v", job, err)
	}

	report, err = importer.Import(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	paths, _ = jobqueue.Pending(cfg)
	if report.Imported != 0 || len(paths) != 1 {
		t.Fatalf("duplicate import report = %+v, jobs = %v", report, paths)
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
	importer.Probe = func(_ context.Context, _ string, path string) (media.Probe, error) {
		if path == source {
			return sourceProbe, nil
		}
		return outputProbe, nil
	}
	report, err := importer.Import(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Imported != 0 || len(report.Items) != 1 || report.Items[0].Status != "already-processed" {
		t.Fatalf("existing result report = %+v", report)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("existing result was staged again: %v", err)
	}
	if paths, _ := jobqueue.Pending(cfg); len(paths) != 0 {
		t.Fatalf("existing result was enqueued again: %v", paths)
	}
}

func TestImporterWaitsForCompletedSettledManifest(t *testing.T) {
	cfg := importerTestConfig(t.TempDir())
	_, manifestPath := writeCompletedRecording(t, cfg, "Интервью_2026-09-07_15-00-00.001", "recording")
	importer := New(cfg)
	importer.Now = time.Now
	report, err := importer.Import(t.Context())
	if err != nil || report.Imported != 0 || report.Skipped != 1 {
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
	if err != nil || report.Imported != 0 || report.Skipped != 1 {
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
