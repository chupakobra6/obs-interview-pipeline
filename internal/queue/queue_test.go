package queue

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
)

func TestQueueLifecycle(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{StateDir: filepath.Join(dir, "state")}
	recording := filepath.Join(dir, "recording.mp4")
	if err := os.WriteFile(recording, []byte("recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	job, err := Enqueue(cfg, recording)
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	paths, err := Pending(cfg)
	if err != nil || len(paths) != 1 {
		t.Fatalf("Pending() = %v, %v", paths, err)
	}
	read, err := Read(paths[0])
	if err != nil || read.ID != job.ID || read.Path != recording {
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
	failedPath := filepath.Join(cfg.FailedDir(), filepath.Base(pathsOrOriginal(cfg, job.ID)))
	failed, err := Read(failedPath)
	if err != nil || failed.Error != jobErr.Error() {
		t.Fatalf("failed job = %#v, %v", failed, err)
	}
}

func pathsOrOriginal(cfg config.Config, id string) string {
	return filepath.Join(cfg.QueueDir(), id+".json")
}
