package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	AppDirName       = "obs-interview-pipeline"
	ConfigFileName   = "config.json"
	LaunchAgentLabel = "com.igor.obs-interview-processor"
)

type Config struct {
	AllowedInputDir       string `json:"allowed_input_dir"`
	OutputDir             string `json:"output_dir"`
	StateDir              string `json:"state_dir"`
	FFmpegCommand         string `json:"ffmpeg_command"`
	FFprobeCommand        string `json:"ffprobe_command"`
	WhisperServerCommand  string `json:"whisper_server_command"`
	WhisperModelPath      string `json:"whisper_model_path"`
	WhisperGateCommand    string `json:"whisper_gate_command"`
	WhisperGateModelPath  string `json:"whisper_gate_model_path"`
	OutputWidth           int    `json:"output_width"`
	OutputHeight          int    `json:"output_height"`
	OutputFPS             int    `json:"output_fps"`
	VideoQuality          int    `json:"video_quality"`
	DeleteSourceOnSuccess bool   `json:"delete_source_on_success"`
	Notifications         bool   `json:"notifications"`
}

func Default(home string) Config {
	appSupport := filepath.Join(home, "Library", "Application Support", AppDirName)
	harvest := filepath.Join(home, "projects", "telegram-harvest")
	whisperBin := filepath.Join(harvest, ".state", "asr-runtime", "whisper.cpp", "build-metal", "bin")
	whisperModels := filepath.Join(harvest, ".state", "asr-runtime", "whisper.cpp", "models")
	return Config{
		AllowedInputDir:       filepath.Join(home, "Movies"),
		OutputDir:             filepath.Join(home, "Movies", "Interviews"),
		StateDir:              appSupport,
		FFmpegCommand:         "/opt/homebrew/bin/ffmpeg",
		FFprobeCommand:        "/opt/homebrew/bin/ffprobe",
		WhisperServerCommand:  filepath.Join(whisperBin, "whisper-server"),
		WhisperModelPath:      filepath.Join(whisperModels, "ggml-large-v3-turbo-q5_0.bin"),
		WhisperGateCommand:    filepath.Join(whisperBin, "whisper-vad-speech-segments"),
		WhisperGateModelPath:  filepath.Join(whisperModels, "ggml-silero-v6.2.0.bin"),
		OutputWidth:           1512,
		OutputHeight:          982,
		OutputFPS:             30,
		VideoQuality:          60,
		DeleteSourceOnSuccess: true,
		Notifications:         true,
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
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
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
	for name, value := range map[string]string{
		"allowed_input_dir":       c.AllowedInputDir,
		"output_dir":              c.OutputDir,
		"state_dir":               c.StateDir,
		"ffmpeg_command":          c.FFmpegCommand,
		"ffprobe_command":         c.FFprobeCommand,
		"whisper_server_command":  c.WhisperServerCommand,
		"whisper_model_path":      c.WhisperModelPath,
		"whisper_gate_command":    c.WhisperGateCommand,
		"whisper_gate_model_path": c.WhisperGateModelPath,
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
	return nil
}
