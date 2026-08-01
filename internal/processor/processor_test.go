package processor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chupakobra6/obs-interview-pipeline/internal/asr"
	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/media"
)

type transcriberFunc func(context.Context, string, string) (asr.Result, error)

func (f transcriberFunc) Transcribe(ctx context.Context, input, work string) (asr.Result, error) {
	return f(ctx, input, work)
}

type compressorFunc func(context.Context, string, string) error

func (f compressorFunc) Compress(ctx context.Context, input, output string) error {
	return f(ctx, input, output)
}

func TestPipelineDeletesOnlyAfterValidatedPublish(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "interview.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{
		Config: cfg,
		Transcriber: transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
			return asr.Result{Text: "Тестовая расшифровка.", SpeechDetected: true, MetalConfirmed: true}, nil
		}),
		Compressor: compressorFunc(func(_ context.Context, _, output string) error {
			return os.WriteFile(output, make([]byte, 500), 0o600)
		}),
		Probe: fakeProbe(source),
		Remove: func(string) error {
			return errors.New("delete denied")
		},
	}
	result, err := pipeline.Process(context.Background(), source)
	if err == nil || result.FinalDir != "" {
		t.Fatalf("Process() = %#v, %v; want deletion error", result, err)
	}
	t.Logf("expected deletion failure: %v", err)
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was removed after delete failure: %v", err)
	}
	finalDir := filepath.Join(cfg.OutputDir, "interview")
	if _, err := os.Stat(filepath.Join(finalDir, "recording.mp4")); err != nil {
		t.Fatalf("validated output was not published: %v", err)
	}

	pipeline.Remove = os.Remove
	result, err = pipeline.Process(context.Background(), source)
	if err != nil {
		t.Fatalf("Process() retry error = %v", err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source remains after successful retry: %v", err)
	}
	if result.FinalDir != finalDir {
		t.Fatalf("FinalDir = %q, want %q", result.FinalDir, finalDir)
	}
}

func TestPipelineFailureKeepsSourceAndPublishesNothing(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "failure.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{
		Config: cfg,
		Transcriber: transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
			return asr.Result{}, errors.New("ASR failed")
		}),
		Compressor: compressorFunc(func(ctx context.Context, _, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		}),
		Probe:  fakeProbe(source),
		Remove: os.Remove,
	}
	if _, err := pipeline.Process(context.Background(), source); err == nil {
		t.Fatal("Process() succeeded, want failure")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was removed on failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "failure")); !os.IsNotExist(err) {
		t.Fatalf("final output was published on failure: %v", err)
	}
}

func testConfig(dir string) config.Config {
	return config.Config{
		AllowedInputDir:       filepath.Join(dir, "input"),
		OutputDir:             filepath.Join(dir, "output"),
		StateDir:              filepath.Join(dir, "state"),
		FFmpegCommand:         "ffmpeg",
		FFprobeCommand:        "ffprobe",
		WhisperServerCommand:  "whisper-server",
		WhisperModelPath:      "model.bin",
		WhisperGateCommand:    "whisper-gate",
		WhisperGateModelPath:  "gate.bin",
		OutputWidth:           1512,
		OutputHeight:          982,
		OutputFPS:             30,
		VideoQuality:          60,
		DeleteSourceOnSuccess: true,
	}
}

func fakeProbe(_ string) ProbeFunc {
	return func(_ context.Context, _, path string) (media.Probe, error) {
		if filepath.Base(path) != "recording.mp4" {
			return media.Probe{
				Streams: []media.Stream{{CodecType: "video", CodecName: "h264"}, {CodecType: "audio", CodecName: "aac"}},
				Format:  media.Format{Duration: "10", Size: "1000"},
			}, nil
		}
		return media.Probe{
			Streams: []media.Stream{{CodecType: "video", CodecName: "hevc", Width: 1512, Height: 982, AvgFrameRate: "30/1"}, {CodecType: "audio", CodecName: "aac"}},
			Format:  media.Format{Duration: "10", Size: "500"},
		}, nil
	}
}
