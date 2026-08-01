package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
)

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
	got := promptArgs("/tmp/processor", "/tmp/config.json", "/Users/igor/Movies/Собес 1.mp4", true)
	want := []string{
		"--processor", "/tmp/processor",
		"--config", "/tmp/config.json",
		"--recording", "/Users/igor/Movies/Собес 1.mp4",
		"--delete-source-default", "true",
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
