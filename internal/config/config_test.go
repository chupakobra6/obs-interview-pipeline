package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadForInstallMigratesLegacyWhisperConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	legacy := `{
  "allowed_input_dir": "/tmp/input",
  "output_dir": "/tmp/output",
  "state_dir": "/tmp/state",
  "ffmpeg_command": "/tmp/ffmpeg",
  "ffprobe_command": "/tmp/ffprobe",
  "whisper_server_command": "/tmp/whisper-server",
  "whisper_model_path": "/tmp/model.bin",
  "whisper_gate_command": "/tmp/gate",
  "whisper_gate_model_path": "/tmp/gate.bin",
  "output_width": 1512,
  "output_height": 982,
  "output_fps": 30,
  "video_quality": 55,
  "delete_source_on_success": true,
  "notifications": true
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadForInstall(path, Default(dir))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != CurrentVersion || cfg.VideoQuality != 55 {
		t.Fatalf("migration lost settings: %+v", cfg)
	}
	if cfg.TelegramHarvestRoot != filepath.Join(dir, "projects", "telegram-harvest") {
		t.Fatalf("unexpected Harvest root: %s", cfg.TelegramHarvestRoot)
	}
	if err := Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "whisper_server_command") {
		t.Fatalf("legacy key survived migration:\n%s", payload)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("load migrated config: %v", err)
	}
}

func TestLoadRejectsUnknownConfigKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Default(dir)
	if err := Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payload = []byte(strings.Replace(string(payload), "\n}", ",\n  \"stale_key\": true\n}", 1))
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unexpected error: %v", err)
	}
}
