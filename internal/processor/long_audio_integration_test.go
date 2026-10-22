package processor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/asr"
	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
)

// This longer, explicitly selected check crosses the incident's 32:30 boundary.
// Ordinary tests keep the equivalent short EOF/content-loss cases.
func TestDisposableLongAudioIntegration(t *testing.T) {
	if os.Getenv("OBS_AUDIO_LONG_TEST") != "1" {
		t.Skip("run make test-audio-long")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(home)
	dir := t.TempDir()
	cfg.AllowedInputDir = filepath.Join(dir, "input")
	cfg.OutputDir = filepath.Join(dir, "output")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.CommandContext(ctx, cfg.FFmpegCommand, args...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v: %s", err, out)
		}
	}
	seed := filepath.Join(dir, "seed.mp4")
	run("-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=black:size=1512x982:rate=30", "-t", "1", "-an", "-c:v", "hevc_videotoolbox", "-b:v", "2500k", seed)
	source := filepath.Join(cfg.AllowedInputDir, "long-short-microphone.mp4")
	run("-nostdin", "-v", "error", "-y", "-stream_loop", "-1", "-i", seed,
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2004,aformat=channel_layouts=stereo",
		"-f", "lavfi", "-i", "sine=frequency=550:sample_rate=48000:duration=2004,aformat=channel_layouts=stereo",
		"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000:duration=1950.3,aformat=channel_layouts=stereo",
		"-t", "2004", "-map", "0:v:0", "-map", "1:a:0", "-map", "2:a:0", "-map", "3:a:0", "-c:v", "copy", "-c:a", "aac", "-b:a", "160k", source)
	p := New(cfg)
	p.Transcriber = transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
		return asr.Result{Text: "Длинная синтетическая проверка.", SpeechDetected: true, MetalConfirmed: true}, nil
	})
	r, err := p.Process(ctx, source, policy.Options{AudioMode: policy.AudioMerge})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("keep-source recording missing: %v", err)
	}
	proof := r.AudioVerification
	if !proof.Complete || proof.SourceTracks != 3 || proof.OutputTracks != 1 || len(proof.OutputSamples) != 1 || proof.OutputSamples[0] < 2003*48000 || proof.ActiveWindows < 10010 {
		t.Fatalf("incomplete long audio proof: %+v", proof)
	}
	if err := proof.ValidateOutput(ctx, r.VideoPath); err != nil {
		t.Fatal(err)
	}
	t.Logf("verified %.3fs, mic %.3fs, output %.3fs, windows=%d, min correlation=%.6f", float64(proof.SourceSamples[0])/48000, float64(proof.SourceSamples[2])/48000, float64(proof.OutputSamples[0])/48000, proof.Windows, proof.MinimumCorrelation)
}
