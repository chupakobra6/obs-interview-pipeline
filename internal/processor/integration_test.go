package processor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/asr"
	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/media"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
)

func TestDisposableOfflineTranscodePreservesFrameCadence(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	output := filepath.Join(dir, "compressed.mp4")
	args := []string{
		"-nostdin", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1512x982:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-f", "lavfi", "-i", "sine=frequency=550:sample_rate=48000",
		"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000",
		"-t", "30", "-map", "0:v:0", "-map", "1:a:0", "-map", "2:a:0", "-map", "3:a:0",
		"-c:v", "hevc_videotoolbox", "-b:v", "354k", "-realtime", "0",
		"-c:a", "aac", "-b:a", "160k", source,
	}
	if data, err := exec.CommandContext(ctx, cfg.FFmpegCommand, args...).CombinedOutput(); err != nil {
		t.Fatalf("create synthetic recording: %v: %s", err, data)
	}
	p := New(cfg)
	inputProbe, err := p.Probe(ctx, cfg.FFprobeCommand, source)
	if err != nil {
		t.Fatal(err)
	}
	compression, err := p.Compressor.Compress(ctx, source, output, inputProbe, policy.Options{AudioMode: policy.AudioPreserve}, EncodeSettings{AudioBitrateKbps: 96, ForceVideoTranscode: true})
	if err != nil {
		t.Fatal(err)
	}
	if compression.VideoMode != "transcode" {
		t.Fatalf("expected offline transcode, got %+v", compression)
	}
	outputProbe, err := p.Probe(ctx, cfg.FFprobeCommand, output)
	if err != nil {
		t.Fatal(err)
	}
	if err := media.ValidateCompressed(inputProbe, outputProbe, cfg.OutputWidth, cfg.OutputHeight, cfg.OutputFPS, 96, 3); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{source, output} {
		data, err := exec.CommandContext(ctx, cfg.FFprobeCommand, "-v", "error", "-select_streams", "v:0", "-count_packets", "-show_entries", "stream=nb_read_packets", "-of", "csv=p=0", path).CombinedOutput()
		if err != nil || strings.TrimSpace(string(data)) != "900" {
			t.Fatalf("frame loss in %s: packets=%q, error=%v; want all 900 frames", path, data, err)
		}
	}
	proof, err := p.VerifyAudio(ctx, cfg.FFmpegCommand, source, output, inputProbe, outputProbe, false)
	if err != nil || !proof.Complete || proof.SourceTracks != 3 || proof.OutputTracks != 3 {
		t.Fatalf("offline transcode did not preserve all audio: proof=%+v error=%v", proof, err)
	}
}

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
	for _, test := range []struct {
		name            string
		audioMode       string
		outputTracks    int
		shortMicrophone bool
		sourceTracks    int
	}{
		{name: "preserve", audioMode: policy.AudioPreserve, outputTracks: 3, sourceTracks: 3},
		{name: "merge", audioMode: policy.AudioMerge, outputTracks: 1, sourceTracks: 3},
		{name: "merge-short-microphone", audioMode: policy.AudioMerge, outputTracks: 1, shortMicrophone: true, sourceTracks: 3},
		{name: "merge-single", audioMode: policy.AudioMerge, outputTracks: 1, sourceTracks: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := filepath.Join(cfg.AllowedInputDir, "synthetic-"+test.name+".mp4")
			microphone := "sine=frequency=550:sample_rate=48000"
			if test.shortMicrophone {
				microphone += ":duration=2"
			}
			args := []string{
				"-hide_banner", "-loglevel", "error", "-y",
				"-f", "lavfi", "-i", "color=c=0x20242b:size=1512x982:rate=30",
				"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
				"-f", "lavfi", "-i", microphone,
				"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000",
				"-t", "6", "-map", "0:v:0",
			}
			for index := 0; index < test.sourceTracks; index++ {
				args = append(args, "-map", fmt.Sprintf("%d:a:0", index+1))
			}
			args = append(args,
				"-c:v", "hevc_videotoolbox", "-b:v", "2500k", "-constant_bit_rate", "1",
				"-c:a", "aac", "-b:a", "160k", source,
			)
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
			if test.audioMode == policy.AudioMerge && !test.shortMicrophone && test.sourceTracks > 1 {
				// Reproduce a full-length AAC stream that loses only its tail.
				// Metadata checks still pass; the original must survive the gate.
				realCompressor := pipeline.Compressor
				pipeline.Compressor = compressorFunc(func(ctx context.Context, input, output string, probe media.Probe, opts policy.Options, settings EncodeSettings) (Compression, error) {
					compression, err := realCompressor.Compress(ctx, input, output, probe, opts, settings)
					if err != nil {
						return compression, err
					}
					muted := output + ".muted.mp4"
					args := []string{"-nostdin", "-v", "error", "-y", "-i", output, "-map", "0", "-c:v", "copy", "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", settings.AudioBitrateKbps), "-af", "volume=0:enable='gte(t,3)'", muted}
					if out, err := exec.CommandContext(ctx, cfg.FFmpegCommand, args...).CombinedOutput(); err != nil {
						return compression, fmt.Errorf("create truncated audio: %w: %s", err, out)
					}
					return compression, os.Rename(muted, output)
				})
				pipeline.Transcriber = transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
					t.Fatal("ASR started with lost audio")
					return asr.Result{}, nil
				})
				if _, err := pipeline.Process(context.Background(), source, policy.Options{DeleteSource: true, AudioMode: test.audioMode}); err == nil || !strings.Contains(err.Error(), "audio preservation failed") {
					t.Fatalf("lost tail passed audio gate: %v", err)
				}
				if _, err := os.Stat(source); err != nil {
					t.Fatalf("audio loss deleted source: %v", err)
				}
				if _, err := os.Stat(filepath.Join(cfg.OutputDir, "synthetic-"+test.name)); !os.IsNotExist(err) {
					t.Fatalf("bad audio was published: %v", err)
				}
				pipeline.Compressor = realCompressor
			}
			pipeline.Transcriber = transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
				return asr.Result{Text: "Синтетическая расшифровка.", SpeechDetected: true, MetalConfirmed: true}, nil
			})
			result, err := pipeline.Process(context.Background(), source, policy.Options{DeleteSource: true, AudioMode: test.audioMode})
			if err != nil {
				t.Fatalf("Process() error = %v", err)
			}
			if _, err := os.Stat(source); !os.IsNotExist(err) {
				t.Fatalf("source was not deleted: %v", err)
			}
			if test.shortMicrophone {
				// The independent PCM reference checks all three distinct input tones,
				// including the two long tracks after the middle decoder reaches EOF.
				proof := result.AudioVerification
				if !proof.Complete || len(proof.SourceSamples) != 3 || proof.SourceSamples[1] > 3*48000 || proof.SourceSamples[0] < 5*48000 || proof.SourceSamples[2] < 5*48000 || proof.OutputSamples[0] < 5*48000 {
					t.Fatalf("separate merge inputs did not retain their independent EOFs: %+v", proof)
				}
			}
			if result.OutputBytes >= result.SourceBytes {
				t.Fatalf("output did not shrink: %d >= %d", result.OutputBytes, result.SourceBytes)
			}
			if result.Compression.VideoMode != "copy" || result.Compression.AudioMode != test.audioMode || result.Compression.OutputAudioTracks != test.outputTracks || result.Compression.AudioBitrateKbps != 96 {
				t.Fatalf("unexpected compression path: %+v", result.Compression)
			}
		})
	}
}
