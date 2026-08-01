package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	CurrentVersion   = 5
	AppDirName       = "obs-interview-pipeline"
	ConfigFileName   = "config.json"
	LaunchAgentLabel = "com.igor.obs-interview-processor"
)

type Config struct {
	Version                int    `json:"version"`
	AllowedInputDir        string `json:"allowed_input_dir"`
	OutputDir              string `json:"output_dir"`
	StateDir               string `json:"state_dir"`
	FFmpegCommand          string `json:"ffmpeg_command"`
	FFprobeCommand         string `json:"ffprobe_command"`
	TelegramHarvestRoot    string `json:"telegram_harvest_root"`
	TelegramHarvestCommand string `json:"telegram_harvest_command"`
	MakeCommand            string `json:"make_command"`
	NotifierCommand        string `json:"notifier_command"`
	PromptCommand          string `json:"prompt_command"`
	OutputWidth            int    `json:"output_width"`
	OutputHeight           int    `json:"output_height"`
	OutputFPS              int    `json:"output_fps"`
	VideoQuality           int    `json:"video_quality"`
	AudioBitrateKbps       int    `json:"audio_bitrate_kbps"`
	DeleteSourceOnSuccess  bool   `json:"delete_source_on_success"`
	Notifications          bool   `json:"notifications"`
}

func Default(home string) Config {
	appSupport := filepath.Join(home, "Library", "Application Support", AppDirName)
	harvest := filepath.Join(home, "projects", "telegram-harvest")
	return Config{
		Version:                CurrentVersion,
		AllowedInputDir:        filepath.Join(home, "Movies"),
		OutputDir:              filepath.Join(home, "Movies", "Interviews"),
		StateDir:               appSupport,
		FFmpegCommand:          "/opt/homebrew/bin/ffmpeg",
		FFprobeCommand:         "/opt/homebrew/bin/ffprobe",
		TelegramHarvestRoot:    harvest,
		TelegramHarvestCommand: filepath.Join(harvest, "bin", "telegram-harvest"),
		MakeCommand:            "/usr/bin/make",
		NotifierCommand:        filepath.Join(appSupport, "OBS Interview Notifier.app", "Contents", "MacOS", "obs-interview-notifier"),
		PromptCommand:          filepath.Join(appSupport, "OBS Interview Prompt.app", "Contents", "MacOS", "obs-interview-prompt"),
		OutputWidth:            1512,
		OutputHeight:           982,
		OutputFPS:              30,
		VideoQuality:           55,
		AudioBitrateKbps:       96,
		DeleteSourceOnSuccess:  true,
		Notifications:          true,
	}
}

func DefaultPath(home string) string {
	return filepath.Join(home, "Library", "Application Support", AppDirName, ConfigFileName)
}

func Load(path string) (Config, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadForInstall overlays an existing configuration on current defaults. It
// is used only by install to migrate older config files and immediately writes
// the normalized current schema back to disk.
func LoadForInstall(path string, defaults Config) (Config, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var stored struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(payload, &stored); err != nil {
		return Config{}, fmt.Errorf("decode existing config version: %w", err)
	}
	cfg := defaults
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode existing config for migration: %w", err)
	}
	cfg.Version = CurrentVersion
	if stored.Version < CurrentVersion {
		cfg.NotifierCommand = defaults.NotifierCommand
		cfg.PromptCommand = defaults.PromptCommand
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Write(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	payload = append(payload, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod temporary config: %w", err)
	}
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish config: %w", err)
	}
	return nil
}

func (c Config) QueueDir() string  { return filepath.Join(c.StateDir, "queue") }
func (c Config) DoneDir() string   { return filepath.Join(c.StateDir, "done") }
func (c Config) FailedDir() string { return filepath.Join(c.StateDir, "failed") }
func (c Config) LogsDir() string   { return filepath.Join(c.StateDir, "logs") }
func (c Config) WorkDir() string   { return filepath.Join(c.StateDir, "work") }

func (c Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("config version must be %d; run install to migrate it", CurrentVersion)
	}
	for name, value := range map[string]string{
		"allowed_input_dir":        c.AllowedInputDir,
		"output_dir":               c.OutputDir,
		"state_dir":                c.StateDir,
		"ffmpeg_command":           c.FFmpegCommand,
		"ffprobe_command":          c.FFprobeCommand,
		"telegram_harvest_root":    c.TelegramHarvestRoot,
		"telegram_harvest_command": c.TelegramHarvestCommand,
		"make_command":             c.MakeCommand,
		"notifier_command":         c.NotifierCommand,
		"prompt_command":           c.PromptCommand,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("config %s is empty", name)
		}
	}
	if c.OutputWidth <= 0 || c.OutputHeight <= 0 || c.OutputWidth%2 != 0 || c.OutputHeight%2 != 0 {
		return fmt.Errorf("output dimensions must be positive even numbers")
	}
	if c.OutputFPS <= 0 || c.OutputFPS > 120 {
		return fmt.Errorf("output_fps must be between 1 and 120")
	}
	if c.VideoQuality < 1 || c.VideoQuality > 100 {
		return fmt.Errorf("video_quality must be between 1 and 100")
	}
	if c.AudioBitrateKbps < 32 || c.AudioBitrateKbps > 320 {
		return fmt.Errorf("audio_bitrate_kbps must be between 32 and 320")
	}
	return nil
}
