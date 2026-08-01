package processor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chupakobra6/obs-interview-pipeline/internal/asr"
	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/media"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
)

type transcriberFunc func(context.Context, string, string) (asr.Result, error)

func (f transcriberFunc) Transcribe(ctx context.Context, input, work string) (asr.Result, error) {
	return f(ctx, input, work)
}

type compressorFunc func(context.Context, string, string, media.Probe, policy.Options) (Compression, error)

func (f compressorFunc) Compress(ctx context.Context, input, output string, source media.Probe, options policy.Options) (Compression, error) {
	return f(ctx, input, output, source, options)
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
		Compressor: compressorFunc(func(_ context.Context, _, output string, _ media.Probe, _ policy.Options) (Compression, error) {
			return Compression{VideoMode: "copy", AudioMode: policy.AudioPreserve, SourceAudioTracks: 1, OutputAudioTracks: 1, AudioBitrateKbps: 96}, os.WriteFile(output, make([]byte, 500), 0o600)
		}),
		Probe: fakeProbe(source),
		Remove: func(string) error {
			return errors.New("delete denied")
		},
	}
	options := policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve}
	result, err := pipeline.Process(context.Background(), source, options)
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
	result, err = pipeline.Process(context.Background(), source, options)
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
		Compressor: compressorFunc(func(ctx context.Context, _, _ string, _ media.Probe, _ policy.Options) (Compression, error) {
			<-ctx.Done()
			return Compression{}, ctx.Err()
		}),
		Probe:  fakeProbe(source),
		Remove: os.Remove,
	}
	if _, err := pipeline.Process(context.Background(), source, policy.Options{AudioMode: policy.AudioPreserve}); err == nil {
		t.Fatal("Process() succeeded, want failure")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was removed on failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "failure")); !os.IsNotExist(err) {
		t.Fatalf("final output was published on failure: %v", err)
	}
}

func TestPipelineKeepsSourceWhenJobDoesNotRequestDeletion(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "keep.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{
		Config: cfg,
		Transcriber: transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
			return asr.Result{Text: "Исходник остаётся.", SpeechDetected: true, MetalConfirmed: true}, nil
		}),
		Compressor: compressorFunc(func(_ context.Context, _, output string, _ media.Probe, _ policy.Options) (Compression, error) {
			return Compression{VideoMode: "copy", AudioMode: policy.AudioPreserve, SourceAudioTracks: 1, OutputAudioTracks: 1, AudioBitrateKbps: 96}, os.WriteFile(output, make([]byte, 500), 0o600)
		}),
		Probe: fakeProbe(source),
		Remove: func(string) error {
			t.Fatal("Remove called for keep-source job")
			return nil
		},
	}
	result, err := pipeline.Process(context.Background(), source, policy.Options{AudioMode: policy.AudioPreserve})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was not preserved: %v", err)
	}
	if result.Options.DeleteSource || result.Options.AudioMode != policy.AudioPreserve {
		t.Fatalf("result options = %+v", result.Options)
	}
}

func testConfig(dir string) config.Config {
	return config.Config{
		Version:                config.CurrentVersion,
		AllowedInputDir:        filepath.Join(dir, "input"),
		OutputDir:              filepath.Join(dir, "output"),
		StateDir:               filepath.Join(dir, "state"),
		FFmpegCommand:          "ffmpeg",
		FFprobeCommand:         "ffprobe",
		TelegramHarvestRoot:    filepath.Join(dir, "telegram-harvest"),
		TelegramHarvestCommand: "telegram-harvest",
		MakeCommand:            "make",
		NotifierCommand:        filepath.Join(dir, "OBS Interview Notifier.app", "Contents", "MacOS", "obs-interview-notifier"),
		PromptCommand:          filepath.Join(dir, "OBS Interview Prompt.app", "Contents", "MacOS", "obs-interview-prompt"),
		OutputWidth:            1512,
		OutputHeight:           982,
		OutputFPS:              30,
		VideoQuality:           55,
		AudioBitrateKbps:       96,
		DeleteSourceOnSuccess:  true,
	}
}

func fakeProbe(_ string) ProbeFunc {
	return func(_ context.Context, _, path string) (media.Probe, error) {
		if filepath.Base(path) != "recording.mp4" {
			return media.Probe{
				Streams: []media.Stream{{CodecType: "video", CodecName: "h264"}, {CodecType: "audio", CodecName: "aac", BitRate: "160000"}},
				Format:  media.Format{Duration: "10", Size: "1000"},
			}, nil
		}
		return media.Probe{
			Streams: []media.Stream{{CodecType: "video", CodecName: "hevc", Width: 1512, Height: 982, AvgFrameRate: "30/1"}, {CodecType: "audio", CodecName: "aac", BitRate: "96000"}},
			Format:  media.Format{Duration: "10", Size: "500"},
		}, nil
	}
}

func TestFFmpegCommandCopiesReadyHEVCAndPreservesAllAudio(t *testing.T) {
	cfg := testConfig(t.TempDir())
	source := media.Probe{Streams: []media.Stream{
		{CodecType: "video", CodecName: "hevc", Width: 1512, Height: 982, AvgFrameRate: "30/1"},
		{CodecType: "audio", CodecName: "aac"},
		{CodecType: "audio", CodecName: "aac"},
		{CodecType: "audio", CodecName: "aac"},
	}}
	compression, args, err := (FFmpegCompressor{Config: cfg}).command("input.mp4", "output.mp4", source, policy.Options{AudioMode: policy.AudioPreserve})
	if err != nil {
		t.Fatal(err)
	}
	if compression.VideoMode != "copy" || len(compression.VideoFilters) != 0 || compression.AudioMode != policy.AudioPreserve || compression.SourceAudioTracks != 3 || compression.OutputAudioTracks != 3 || compression.AudioBitrateKbps != 96 {
		t.Fatalf("unexpected compression: %+v", compression)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-map 0:a", "-c:v copy", "-c:a aac", "-b:a 96k", "-movflags +faststart"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ffmpeg args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, " -vf ") || strings.Contains(joined, "hevc_videotoolbox") {
		t.Fatalf("ready HEVC unexpectedly filtered or transcoded: %q", joined)
	}
}

func TestFFmpegCommandAddsOnlyNecessaryFallbackFilters(t *testing.T) {
	cfg := testConfig(t.TempDir())
	source := media.Probe{Streams: []media.Stream{
		{CodecType: "video", CodecName: "h264", Width: 3024, Height: 1964, AvgFrameRate: "30/1"},
		{CodecType: "audio", CodecName: "aac"},
	}}
	compression, args, err := (FFmpegCompressor{Config: cfg}).command("input.mp4", "output.mp4", source, policy.Options{AudioMode: policy.AudioPreserve})
	if err != nil {
		t.Fatal(err)
	}
	if compression.VideoMode != "transcode" || len(compression.VideoFilters) != 1 || compression.VideoFilters[0] != "scale=1512:982:flags=lanczos" {
		t.Fatalf("unexpected compression: %+v", compression)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-vf scale=1512:982:flags=lanczos") || strings.Contains(joined, "fps=30") {
		t.Fatalf("unexpected filters: %q", joined)
	}
}

func TestFFmpegCommandMergesAllAudioTracks(t *testing.T) {
	cfg := testConfig(t.TempDir())
	source := media.Probe{Streams: []media.Stream{
		{CodecType: "video", CodecName: "hevc", Width: 1512, Height: 982, AvgFrameRate: "30/1"},
		{CodecType: "audio", CodecName: "aac"},
		{CodecType: "audio", CodecName: "aac"},
		{CodecType: "audio", CodecName: "aac"},
	}}
	compression, args, err := (FFmpegCompressor{Config: cfg}).command("input.mp4", "output.mp4", source, policy.Options{AudioMode: policy.AudioMerge})
	if err != nil {
		t.Fatal(err)
	}
	if compression.AudioMode != policy.AudioMerge || compression.SourceAudioTracks != 3 || compression.OutputAudioTracks != 1 {
		t.Fatalf("unexpected compression: %+v", compression)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"[0:a:0][0:a:1][0:a:2]amix=inputs=3", "-map [aout]", "normalize=1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ffmpeg args %q missing %q", joined, want)
		}
	}
}
