package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
)

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
