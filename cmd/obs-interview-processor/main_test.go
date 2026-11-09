package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
	jobqueue "github.com/chupakobra6/obs-interview-pipeline/internal/queue"
)

func TestQueueCommandsShareLockAndDoNotReplayFinishedJob(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	cfg.Notifications = false
	cfgPath := filepath.Join(home, "config.json")
	if err := config.Write(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	recording := filepath.Join(home, "recording.mp4")
	if err := os.WriteFile(recording, []byte("synthetic recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	call := func(command string, positional ...string) (int, string, string) {
		stdout.Reset()
		stderr.Reset()
		code := run(append([]string{command, "--config", cfgPath}, positional...), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	if code, out, msg := call("status", "missing"); code != 1 || out != "" || !strings.Contains(msg, "not found") {
		t.Fatalf("missing status: %d %q %q", code, out, msg)
	}
	if _, err := os.Stat(cfg.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status created state: %v", err)
	}
	if code, out, msg := call("status", "../missing"); code != 2 || out != "" || !strings.Contains(msg, "basename") {
		t.Fatalf("invalid status: %d %q %q", code, out, msg)
	}
	code, out, msg := call("enqueue", recording)
	var job jobqueue.Job
	if err := json.Unmarshal([]byte(out), &job); code != 0 || err != nil || job.ID == "" {
		t.Fatalf("enqueue: %d %q %q (%v)", code, out, msg, err)
	}
	checkStatus := func(want string) jobqueue.Status {
		t.Helper()
		code, out, msg := call("status", job.ID)
		var status jobqueue.Status
		if err := json.Unmarshal([]byte(out), &status); code != 0 || err != nil || status.Status != want || status.ID != job.ID || status.Path != recording {
			t.Fatalf("status wants %q: %d %q %q (%v)", want, code, out, msg, err)
		}
		return status
	}
	if err := jobqueue.WithLock(cfg, func() error {
		if code, out, msg := call("process", recording); code != 1 || out != "" || !strings.Contains(msg, "processor is busy") {
			t.Fatalf("busy process: %d %q %q", code, out, msg)
		}
		if code, out, msg := call("run-queue"); code != 0 || out != "" || msg != "" {
			t.Fatalf("busy worker: %d %q %q", code, out, msg)
		}
		checkStatus("queued")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(cfg.QueueDir(), job.ID+".json")
	payload, err := os.ReadFile(queuePath)
	if err != nil {
		t.Fatal(err)
	}
	job.Output = &jobqueue.Output{
		FinalDir: filepath.Join(cfg.OutputDir, "recording"), VideoPath: filepath.Join(cfg.OutputDir, "recording", "recording.mp4"),
		TranscriptPath: filepath.Join(cfg.OutputDir, "recording", "transcript.md"), ManifestPath: filepath.Join(cfg.OutputDir, "recording", "manifest.json"),
	}
	if err := jobqueue.Finish(cfg, queuePath, job, nil); err != nil {
		t.Fatal(err)
	}
	// Model an interruption after publishing the terminal receipt but before
	// removing the original queue entry. The dummy media cannot be processed.
	if err := os.WriteFile(queuePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if status := checkStatus("done"); !reflect.DeepEqual(status.Output, job.Output) || status.FinishedAt.IsZero() {
		t.Fatalf("wrong success receipt: %+v", status)
	}
	if code, out, msg := call("run-queue"); code != 0 || out != "" || msg != "" {
		t.Fatalf("worker replayed completed job: %d %q %q", code, out, msg)
	}
	if _, err := os.Stat(queuePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("queue entry still present: %v", err)
	}
	checkStatus("done")
}

func TestRunQueueKeepsJobsPendingOnCancellation(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	cfg.Notifications = false
	cfg.FFprobeCommand = filepath.Join(home, "ffprobe")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.FFprobeCommand, []byte("#!/bin/sh\n: > \"$0.started\"\nexec /bin/sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var jobs []jobqueue.Job
	for _, name := range []string{"first.mp4", "second.mp4"} {
		path := filepath.Join(cfg.AllowedInputDir, name)
		if err := os.WriteFile(path, []byte("synthetic recording"), 0o600); err != nil {
			t.Fatal(err)
		}
		job, err := jobqueue.Enqueue(cfg, path, policy.Options{AudioMode: policy.AudioPreserve})
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	checkStatuses := func(want string) {
		t.Helper()
		for _, job := range jobs {
			status, err := jobqueue.Lookup(cfg, job.ID)
			if err != nil || status.Status != want {
				t.Fatalf("job %s wants %q: %#v, %v", job.ID, want, status, err)
			}
		}
	}
	stoppedCtx, stop := context.WithCancel(context.Background())
	stop()
	var stdout bytes.Buffer
	if err := runQueue(stoppedCtx, cfg, &stdout, io.Discard); !errors.Is(err, context.Canceled) || stdout.Len() != 0 {
		t.Fatalf("already canceled worker attempted work: %v, %q", err, stdout.String())
	}
	checkStatuses("queued")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- runQueue(ctx, cfg, io.Discard, io.Discard) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
waitForProbe:
	for {
		select {
		case err := <-finished:
			t.Fatalf("worker exited before cancellation: %v", err)
		case <-ctx.Done():
			t.Fatal("worker did not reach the probe")
		case <-ticker.C:
			if _, err := os.Stat(cfg.FFprobeCommand + ".started"); err == nil {
				break waitForProbe
			}
		}
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted worker returned %v", err)
	}
	checkStatuses("queued")

	if err := os.WriteFile(cfg.FFprobeCommand, []byte("#!/bin/sh\necho 'synthetic probe failure' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runQueue(context.Background(), cfg, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	checkStatuses("failed")
}

func TestCheckInstalledBuilds(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	local := filepath.Join(home, "local-processor")
	worker, importer := installedWorkerPath(cfg), installedImporterPath(cfg)
	if err := os.MkdirAll(filepath.Dir(worker), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{local, worker, importer} {
		if err := os.WriteFile(path, []byte("current build"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkInstalledBuilds(cfg, local); err != nil {
		t.Fatalf("identical builds rejected: %v", err)
	}
	if err := os.WriteFile(worker, []byte("changed build"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(importer); err != nil {
		t.Fatal(err)
	}
	err := checkInstalledBuilds(cfg, local)
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), worker) || !strings.Contains(err.Error(), "differs") || !strings.Contains(err.Error(), importer) {
		t.Fatalf("mismatch and missing installation were not reported: %v", err)
	}
}

func TestOBSHookUsesInstalledWorkerWithObsoleteSavedPath(t *testing.T) {
	lua, err := exec.LookPath("lua")
	if err != nil {
		t.Skip("Lua interpreter is not installed")
	}
	worker := installedWorkerPath(config.Default("/Users/igor"))
	source := strings.ReplaceAll(luaHookTemplate, "__PROCESSOR_PATH__", luaEscape(worker))
	source = strings.ReplaceAll(source, "__CONFIG_PATH__", "/tmp/default.json")
	path := filepath.Join(t.TempDir(), "hook.lua")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(lua, "-", path, worker)
	command.Stdin = strings.NewReader(`
local callback, executed
obslua = {
    OBS_FRONTEND_EVENT_RECORDING_STOPPED = 1,
    obs_data_get_string = function(settings, key) return settings[key] end,
    obs_frontend_add_event_callback = function(value) callback = value end,
    obs_frontend_get_last_recording = function() return "/tmp/Собес 1.mp4" end,
    script_log = function() end,
}
os.execute = function(value) executed = value; return true end
dofile(arg[1])
script_load({processor_path = "/missing/old/obs-interview-processor", config_path = "/tmp/custom.json"})
callback(obslua.OBS_FRONTEND_EVENT_RECORDING_STOPPED)
assert(executed == "'" .. arg[2] .. "' prompt --config '/tmp/custom.json' '/tmp/Собес 1.mp4' >/dev/null 2>&1 &", executed)
`)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("OBS hook used an obsolete saved processor path: %v: %s", err, output)
	}
}

func TestNotificationArgsOpenExactFolder(t *testing.T) {
	args := notificationArgs("Готово", "Видео готово", "/Users/igor/Movies/Interviews/Собес 1")
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, "--open-dir\n/Users/igor/Movies/Interviews/Собес 1") {
		t.Fatalf("notification target missing: %q", args)
	}
	if !strings.Contains(joined, "--title\nГотово\n--message\nВидео готово") {
		t.Fatalf("notification text missing: %q", args)
	}
}

func TestPromptArgsPassExactRecordingAndDefaults(t *testing.T) {
	got := promptArgs("/tmp/processor", "/tmp/config.json", "/Users/igor/Movies/Собес 1.mp4", true, false)
	want := []string{
		"--processor", "/tmp/processor",
		"--config", "/tmp/config.json",
		"--recording", "/Users/igor/Movies/Собес 1.mp4",
		"--delete-source-default", "true",
		"--discard-on-skip", "false",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("promptArgs() = %#v, want %#v", got, want)
	}
}

func TestLoadJobArgsUsesConfigDefaultAndExplicitChoices(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	cfg.DeleteSourceOnSuccess = true
	cfgPath := filepath.Join(home, "config.json")
	if err := config.Write(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	_, defaults, rest, err := loadJobArgs(home, "test", []string{"--config", cfgPath, "recording.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if !defaults.DeleteSource || defaults.AudioMode != policy.AudioPreserve || !reflect.DeepEqual(rest, []string{"recording.mp4"}) {
		t.Fatalf("default options = %+v, rest = %#v", defaults, rest)
	}

	_, explicit, _, err := loadJobArgs(home, "test", []string{"--config", cfgPath, "--delete-source=false", "--audio-mode=merge", "recording.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.DeleteSource || explicit.AudioMode != policy.AudioMerge {
		t.Fatalf("explicit options = %+v", explicit)
	}
}

func TestApplicationPath(t *testing.T) {
	command := "/Users/igor/Library/Application Support/obs-interview-pipeline/OBS Interview Notifier.app/Contents/MacOS/obs-interview-notifier"
	want := "/Users/igor/Library/Application Support/obs-interview-pipeline/OBS Interview Notifier.app"
	if got := applicationPath(command); got != want {
		t.Fatalf("applicationPath() = %q, want %q", got, want)
	}
}

func TestLaunchAgentUsesInteractiveProcessType(t *testing.T) {
	plist := launchAgentPlist("/tmp/processor", "/tmp/config.json", config.Default("/Users/igor"))
	if !strings.Contains(plist, "<key>ProcessType</key>\n  <string>Interactive</string>") {
		t.Fatalf("LaunchAgent does not preserve foreground processing QoS: %s", plist)
	}
	if strings.Contains(plist, "<string>Background</string>") {
		t.Fatalf("LaunchAgent still requests background throttling: %s", plist)
	}
}

func TestSobesTechLaunchAgentWatchesRecordingsWithFallback(t *testing.T) {
	cfg := config.Default("/Users/igor")
	plist := sobesTechLaunchAgentPlist("/tmp/processor", "/tmp/config.json", cfg)
	for _, expected := range []string{
		"<string>import-sobestech</string>",
		"<key>WatchPaths</key>",
		xmlEscape(cfg.SobesTechInputDir),
		"<key>StartInterval</key>\n  <integer>30</integer>",
		"<key>ProcessType</key>\n  <string>Interactive</string>",
	} {
		if !strings.Contains(plist, expected) {
			t.Fatalf("SobesTech LaunchAgent missing %q: %s", expected, plist)
		}
	}
}
