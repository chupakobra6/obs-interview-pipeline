package processor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chupakobra6/obs-interview-pipeline/internal/asr"
	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
)

func TestDisposableFFmpegIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(home)
	if output, err := exec.Command(cfg.FFmpegCommand, "-hide_banner", "-encoders").CombinedOutput(); err != nil || !strings.Contains(string(output), "hevc_videotoolbox") {
		t.Skip("hevc_videotoolbox unavailable")
	}
	dir := t.TempDir()
	cfg.AllowedInputDir = filepath.Join(dir, "input")
	cfg.OutputDir = filepath.Join(dir, "output")
	cfg.StateDir = filepath.Join(dir, "state")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(cfg.AllowedInputDir, "synthetic.mp4")
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=0x20242b:size=1512x982:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-f", "lavfi", "-i", "sine=frequency=550:sample_rate=48000",
		"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000",
		"-t", "6", "-map", "0:v:0", "-map", "1:a:0", "-map", "2:a:0", "-map", "3:a:0",
		"-c:v", "hevc_videotoolbox", "-b:v", "2500k", "-constant_bit_rate", "1",
		"-c:a", "aac", "-b:a", "160k", source,
	}
	if output, err := exec.Command(cfg.FFmpegCommand, args...).CombinedOutput(); err != nil {
		t.Fatalf("create synthetic recording: %v: %s", err, output)
	}
	file, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(make([]byte, 2<<20))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("pad synthetic source: %v, %v", writeErr, closeErr)
	}
	pipeline := New(cfg)
	pipeline.Transcriber = transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
		return asr.Result{Text: "Синтетическая расшифровка.", SpeechDetected: true, MetalConfirmed: true}, nil
	})
	result, err := pipeline.Process(context.Background(), source)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source was not deleted: %v", err)
	}
	if result.OutputBytes >= result.SourceBytes {
		t.Fatalf("output did not shrink: %d >= %d", result.OutputBytes, result.SourceBytes)
	}
	if result.Compression.VideoMode != "copy" || result.Compression.AudioTrack != 1 || result.Compression.AudioBitrateKbps != 96 {
		t.Fatalf("unexpected compression path: %+v", result.Compression)
	}
}
