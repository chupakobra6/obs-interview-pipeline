package queue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
)

type Job struct {
	ID         string    `json:"id"`
	Path       string    `json:"path"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func Enqueue(cfg config.Config, inputPath string) (Job, error) {
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
	job := Job{ID: id, Path: absPath, EnqueuedAt: time.Now()}
	if err := os.MkdirAll(cfg.QueueDir(), 0o700); err != nil {
		return Job{}, fmt.Errorf("create queue directory: %w", err)
	}
	path := filepath.Join(cfg.QueueDir(), id+".json")
	if err := writeExclusiveJSON(path, job); err != nil {
		return Job{}, err
	}
	return job, nil
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

func Finish(cfg config.Config, queuePath string, job Job, runErr error) error {
	job.FinishedAt = time.Now()
	destinationDir := cfg.DoneDir()
	if runErr != nil {
		destinationDir = cfg.FailedDir()
		job.Error = runErr.Error()
	}
	if err := os.MkdirAll(destinationDir, 0o700); err != nil {
		return fmt.Errorf("create job destination: %w", err)
	}
	destination := filepath.Join(destinationDir, filepath.Base(queuePath))
	temp, err := os.CreateTemp(destinationDir, ".job-*.json")
	if err != nil {
		return fmt.Errorf("create job result: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	payload, err := json.MarshalIndent(job, "", "  ")
	if err == nil {
		payload = append(payload, '\n')
		_, err = temp.Write(payload)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return fmt.Errorf("write job result: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close job result: %w", closeErr)
	}
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
		if err == syscall.EWOULDBLOCK {
			return nil
		}
		return fmt.Errorf("lock queue: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return action()
}

func writeExclusiveJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode queued job: %w", err)
	}
	payload = append(payload, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create queued job: %w", err)
	}
	_, writeErr := file.Write(payload)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write queued job: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close queued job: %w", closeErr)
	}
	return nil
}
