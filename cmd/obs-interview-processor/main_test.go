package main

import (
	"strings"
	"testing"
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

func TestNotifierApplicationPath(t *testing.T) {
	command := "/Users/igor/Library/Application Support/obs-interview-pipeline/OBS Interview Notifier.app/Contents/MacOS/obs-interview-notifier"
	want := "/Users/igor/Library/Application Support/obs-interview-pipeline/OBS Interview Notifier.app"
	if got := notifierApplicationPath(command); got != want {
		t.Fatalf("notifierApplicationPath() = %q, want %q", got, want)
	}
}
