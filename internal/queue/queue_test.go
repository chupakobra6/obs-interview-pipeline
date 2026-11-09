package queue

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
)

func TestQueueLifecycle(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{StateDir: filepath.Join(dir, "state")}
	recording := filepath.Join(dir, "recording.mp4")
	if err := os.WriteFile(recording, []byte("recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := policy.Options{DeleteSource: false, AudioMode: policy.AudioPreserve}
	job, err := Enqueue(cfg, recording, options)
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	paths, err := Pending(cfg)
	if err != nil || len(paths) != 1 {
		t.Fatalf("Pending() = %v, %v", paths, err)
	}
	read, err := Read(paths[0])
	if err != nil || read.ID != job.ID || read.Path != recording || read.Options != options {
		t.Fatalf("Read() = %#v, %v", read, err)
	}
	jobErr := errors.New("synthetic failure")
	if err := Finish(cfg, paths[0], job, jobErr); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	paths, err = Pending(cfg)
	if err != nil || len(paths) != 0 {
		t.Fatalf("Pending() after finish = %v, %v", paths, err)
	}
	failedPath := filepath.Join(cfg.FailedDir(), job.ID+".json")
	failed, err := Read(failedPath)
	if err != nil || failed.Error != jobErr.Error() {
		t.Fatalf("failed job = %#v, %v", failed, err)
	}
	status, err := Lookup(cfg, job.ID)
	if err != nil || status.Status != "failed" || status.Error != jobErr.Error() || status.Output != nil {
		t.Fatalf("failed status = %#v, %v", status, err)
	}
}

func TestPublishQueuedJobDoesNotOverwrite(t *testing.T) {
	cfg := config.Config{StateDir: t.TempDir()}
	job := Job{ID: "same-id", Path: "/tmp/original.mp4", Options: policy.Options{AudioMode: policy.AudioPreserve}}
	if err := publishQueuedJob(cfg, job); err != nil {
		t.Fatal(err)
	}
	replacement := job
	replacement.Path = "/tmp/replacement.mp4"
	if err := publishQueuedJob(cfg, replacement); !errors.Is(err, os.ErrExist) {
		t.Fatalf("publish with existing ID: %v", err)
	}
	status, err := Lookup(cfg, job.ID)
	if err != nil || status.Status != "queued" || status.Path != job.Path || status.Options != job.Options {
		t.Fatalf("original job was not preserved: %#v, %v", status, err)
	}
}

func TestLookupLegacyRejectedJob(t *testing.T) {
	cfg := config.Config{StateDir: t.TempDir()}
	if err := os.MkdirAll(cfg.FailedDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.FailedDir(), "rejected.json"), []byte(`{"id":"rejected","path":"<invalid>","options":{"delete_source":false,"audio_mode":""},"finished_at":"2026-11-09T12:00:00+03:00","error":"decode job: unexpected end of JSON input"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := Lookup(cfg, "rejected")
	if err != nil || status.Status != "failed" || status.Error != "decode job: unexpected end of JSON input" || status.Output != nil {
		t.Fatalf("legacy rejected status = %#v, %v", status, err)
	}
}
