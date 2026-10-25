package processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/asr"
	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/media"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
)

type transcriberFunc func(context.Context, string, string) (asr.Result, error)

func (f transcriberFunc) Transcribe(ctx context.Context, input, work string) (asr.Result, error) {
	return f(ctx, input, work)
}

type compressorFunc func(context.Context, string, string, media.Probe, policy.Options, EncodeSettings) (Compression, error)

func (f compressorFunc) Compress(ctx context.Context, input, output string, source media.Probe, options policy.Options, settings EncodeSettings) (Compression, error) {
	return f(ctx, input, output, source, options, settings)
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
	pipeline := successfulPipeline(cfg, source, func(string) error {
		return errors.New("delete denied")
	})
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

func TestPipelineRetryAcceptsAlreadyDeletedSourceAfterValidatedPublish(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "already-deleted.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline := successfulPipeline(cfg, source, func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return errors.New("simulated crash after unlink")
	})
	options := policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve}
	if _, err := pipeline.Process(context.Background(), source, options); err == nil {
		t.Fatal("Process() succeeded despite simulated post-unlink crash")
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source remains after simulated unlink: %v", err)
	}
	pipeline.Remove = func(string) error {
		t.Fatal("retry attempted to delete an already absent source")
		return nil
	}
	result, err := pipeline.Process(context.Background(), source, options)
	if err != nil {
		t.Fatalf("Process() retry error = %v", err)
	}
	if result.SourceBytes != 1000 || result.OutputBytes != 500 {
		t.Fatalf("retry result sizes = %d -> %d", result.SourceBytes, result.OutputBytes)
	}
}

func TestPipelineRetryRejectsChangedSourceIdentity(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "replaced.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline := successfulPipeline(cfg, source, func(string) error {
		return errors.New("delete denied")
	})
	options := policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve}
	if _, err := pipeline.Process(context.Background(), source, options); err == nil {
		t.Fatal("Process() succeeded, want initial deletion failure")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1200), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline.Remove = func(string) error {
		t.Fatal("retry attempted to delete a replacement source")
		return nil
	}
	if _, err := pipeline.Process(context.Background(), source, options); err == nil || !strings.Contains(err.Error(), "source identity changed") {
		t.Fatalf("Process() retry error = %v, want source identity rejection", err)
	}
	if info, err := os.Stat(source); err != nil || info.Size() != 1200 {
		t.Fatalf("replacement source was not preserved: info=%v err=%v", info, err)
	}
}

func TestPipelineRefusesPublishWhenSourceChangesDuringProcessing(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "changed-during-processing.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline := successfulPipeline(cfg, source, func(string) error {
		t.Fatal("changed source must not be deleted")
		return nil
	})
	pipeline.Compressor = compressorFunc(func(_ context.Context, input, output string, _ media.Probe, _ policy.Options, _ EncodeSettings) (Compression, error) {
		if err := os.WriteFile(input, make([]byte, 1200), 0o600); err != nil {
			return Compression{}, err
		}
		compression := Compression{VideoMode: "copy", AudioMode: policy.AudioPreserve, SourceAudioTracks: 1, OutputAudioTracks: 1, AudioBitrateKbps: 96}
		return compression, os.WriteFile(output, make([]byte, 500), 0o600)
	})
	options := policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve}
	if _, err := pipeline.Process(context.Background(), source, options); err == nil || !strings.Contains(err.Error(), "source changed during processing") {
		t.Fatalf("Process() error = %v, want source change rejection", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "changed-during-processing")); !os.IsNotExist(err) {
		t.Fatalf("changed source result was published: %v", err)
	}
	if info, err := os.Stat(source); err != nil || info.Size() != 1200 {
		t.Fatalf("changed source was not preserved: info=%v err=%v", info, err)
	}
}

func TestPipelineRetryRejectsTranscriptThatDiffersFromManifest(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "corrupt-transcript.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline := successfulPipeline(cfg, source, func(string) error {
		return errors.New("delete denied")
	})
	options := policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve}
	if _, err := pipeline.Process(context.Background(), source, options); err == nil {
		t.Fatal("Process() succeeded, want initial deletion failure")
	}
	transcriptPath := filepath.Join(cfg.OutputDir, "corrupt-transcript", "transcript.md")
	if err := os.WriteFile(transcriptPath, []byte("# damaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline.Remove = func(string) error {
		t.Fatal("retry attempted deletion with a corrupt transcript")
		return nil
	}
	if _, err := pipeline.Process(context.Background(), source, options); err == nil || !strings.Contains(err.Error(), "transcript differs") {
		t.Fatalf("Process() retry error = %v, want transcript mismatch", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was not preserved: %v", err)
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
		Compressor: compressorFunc(func(_ context.Context, _, output string, _ media.Probe, _ policy.Options, _ EncodeSettings) (Compression, error) {
			compression := Compression{VideoMode: "copy", AudioMode: policy.AudioPreserve, SourceAudioTracks: 1, OutputAudioTracks: 1, AudioBitrateKbps: 96}
			return compression, os.WriteFile(output, make([]byte, 500), 0o600)
		}),
		Probe:       fakeProbe(source),
		VerifyAudio: fakeAudioVerifier,
		Remove:      os.Remove,
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

func TestPipelineRetryReusesValidatedCompressionCheckpoint(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "retry.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	compressions := 0
	transcriptions := 0
	pipeline := Pipeline{
		Config: cfg,
		Transcriber: transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
			transcriptions++
			if transcriptions == 1 {
				return asr.Result{}, errors.New("retryable ASR failure")
			}
			return asr.Result{Text: "Готово.", SpeechDetected: true, MetalConfirmed: true}, nil
		}),
		Compressor: compressorFunc(func(_ context.Context, _, output string, _ media.Probe, _ policy.Options, _ EncodeSettings) (Compression, error) {
			compressions++
			compression := Compression{VideoMode: "transcode", VideoFilters: []string{"scale=1512:982:flags=lanczos"}, AudioMode: policy.AudioPreserve, SourceAudioTracks: 1, OutputAudioTracks: 1, AudioBitrateKbps: 96}
			return compression, os.WriteFile(output, make([]byte, 500), 0o600)
		}),
		Probe:       fakeProbe(source),
		VerifyAudio: fakeAudioVerifier,
		Remove:      os.Remove,
	}
	options := policy.Options{AudioMode: policy.AudioPreserve}
	if _, err := pipeline.Process(context.Background(), source, options); err == nil || !strings.Contains(err.Error(), "retryable ASR failure") {
		t.Fatalf("first Process() error = %v", err)
	}
	resolvedSource, err := pipeline.resolveInputPath(source)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(pipeline.processingDir(resolvedSource), "compression-checkpoint.json")
	if _, err := os.Stat(checkpoint); err != nil {
		t.Fatalf("compression checkpoint is unavailable after ASR failure: %v", err)
	}
	result, err := pipeline.Process(context.Background(), source, options)
	if err != nil {
		t.Fatalf("retry Process() error = %v", err)
	}
	if compressions != 1 || transcriptions != 2 {
		t.Fatalf("stage calls = compression %d, transcription %d; want 1, 2", compressions, transcriptions)
	}
	if result.OutputBytes != 500 {
		t.Fatalf("retry output bytes = %d", result.OutputBytes)
	}
	if _, err := os.Stat(filepath.Join(result.FinalDir, "compression-checkpoint.json")); !os.IsNotExist(err) {
		t.Fatalf("internal checkpoint was published: %v", err)
	}
}

func TestPipelineCompressionFinishesBeforeTranscriptionStarts(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "sequential.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	compressed := false
	pipeline := Pipeline{
		Config: cfg,
		Transcriber: transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
			if !compressed {
				t.Fatal("transcription overlapped compression")
			}
			return asr.Result{Text: "Последовательно.", SpeechDetected: true, MetalConfirmed: true}, nil
		}),
		Compressor: compressorFunc(func(_ context.Context, _, output string, _ media.Probe, _ policy.Options, _ EncodeSettings) (Compression, error) {
			compressed = true
			compression := Compression{VideoMode: "transcode", AudioMode: policy.AudioPreserve, SourceAudioTracks: 1, OutputAudioTracks: 1, AudioBitrateKbps: 96}
			return compression, os.WriteFile(output, make([]byte, 500), 0o600)
		}),
		Probe:       fakeProbe(source),
		VerifyAudio: fakeAudioVerifier,
		Remove:      os.Remove,
	}
	if _, err := pipeline.Process(context.Background(), source, policy.Options{AudioMode: policy.AudioPreserve}); err != nil {
		t.Fatal(err)
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
		Compressor: compressorFunc(func(_ context.Context, _, output string, _ media.Probe, _ policy.Options, _ EncodeSettings) (Compression, error) {
			return Compression{VideoMode: "copy", AudioMode: policy.AudioPreserve, SourceAudioTracks: 1, OutputAudioTracks: 1, AudioBitrateKbps: 96}, os.WriteFile(output, make([]byte, 500), 0o600)
		}),
		Probe:       fakeProbe(source),
		VerifyAudio: fakeAudioVerifier,
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
		MaxOutputSizePercent:   80,
		AudioBitrateKbps:       96,
		DeleteSourceOnSuccess:  true,
	}
}

func successfulPipeline(cfg config.Config, source string, remove RemoveFunc) Pipeline {
	return Pipeline{
		Config: cfg,
		Transcriber: transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
			return asr.Result{Text: "Тестовая расшифровка.", SpeechDetected: true, MetalConfirmed: true}, nil
		}),
		Compressor: compressorFunc(func(_ context.Context, _, output string, _ media.Probe, _ policy.Options, _ EncodeSettings) (Compression, error) {
			return Compression{VideoMode: "copy", AudioMode: policy.AudioPreserve, SourceAudioTracks: 1, OutputAudioTracks: 1, AudioBitrateKbps: 96}, os.WriteFile(output, make([]byte, 500), 0o600)
		}),
		Probe:       fakeProbe(source),
		VerifyAudio: fakeAudioVerifier,
		Remove:      remove,
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
	compression, args, err := (FFmpegCompressor{Config: cfg}).command("input.mp4", "output.mp4", source, policy.Options{AudioMode: policy.AudioPreserve}, EncodeSettings{AudioBitrateKbps: cfg.AudioBitrateKbps})
	if err != nil {
		t.Fatal(err)
	}
	if compression.VideoMode != "copy" || len(compression.VideoFilters) != 0 || compression.AudioMode != policy.AudioPreserve || compression.SourceAudioTracks != 3 || compression.OutputAudioTracks != 3 || compression.AudioBitrateKbps != 96 {
		t.Fatalf("unexpected compression: %+v", compression)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-map 1:a:0", "-map 2:a:1", "-map 3:a:2", "-c:v copy", "-c:a aac", "-b:a 96k", "-movflags +faststart", "-stats_period 1 -progress output.mp4.progress"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ffmpeg args %q missing %q", joined, want)
		}
	}
	if got := strings.Count(joined, "-i input.mp4"); got != 4 {
		t.Fatalf("preserve uses %d inputs, want video + three independent audio inputs: %s", got, joined)
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
	}, Format: media.Format{Duration: "60", Size: "6000000"}}
	compression, args, err := (FFmpegCompressor{Config: cfg}).command("input.mp4", "output.mp4", source, policy.Options{AudioMode: policy.AudioPreserve}, EncodeSettings{AudioBitrateKbps: cfg.AudioBitrateKbps})
	if err != nil {
		t.Fatal(err)
	}
	if compression.VideoMode != "transcode" || len(compression.VideoFilters) != 1 || compression.VideoFilters[0] != "scale=1512:982:flags=lanczos" {
		t.Fatalf("unexpected compression: %+v", compression)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-vf scale=1512:982:flags=lanczos") || !strings.Contains(joined, "-b:v 528k -maxrate 528k -bufsize 1056k") || !strings.Contains(joined, "-realtime 0 -prio_speed 1") || strings.Contains(joined, "fps=30") || strings.Contains(joined, "-q:v") {
		t.Fatalf("unexpected filters: %q", joined)
	}
}

func TestStageTimeoutsBoundSlowWork(t *testing.T) {
	if got := compressionTimeout(60); got != minimumCompressionTimeout {
		t.Fatalf("short compression timeout = %s", got)
	}
	if got := compressionTimeout(3600); got != 15*time.Minute {
		t.Fatalf("long compression timeout = %s", got)
	}
	if got := transcriptionTimeout(3600); got != 12*time.Minute {
		t.Fatalf("long transcription timeout = %s", got)
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
	compression, args, err := (FFmpegCompressor{Config: cfg}).command("input.mp4", "output.mp4", source, policy.Options{AudioMode: policy.AudioMerge}, EncodeSettings{AudioBitrateKbps: cfg.AudioBitrateKbps})
	if err != nil {
		t.Fatal(err)
	}
	if compression.AudioMode != policy.AudioMerge || compression.SourceAudioTracks != 3 || compression.OutputAudioTracks != 1 {
		t.Fatalf("unexpected compression: %+v", compression)
	}
	joined := strings.Join(args, " ")
	if got := strings.Count(joined, "-i input.mp4"); got != 4 {
		t.Fatalf("merge uses %d demux inputs, want independent video + three audio inputs: %s", got, joined)
	}
	for _, want := range []string{"[1:a:0]aresample=", "[2:a:1]aresample=", "[3:a:2]aresample=", "-map 0:v:0", "-map_metadata 0 -map_chapters 0", "[a0][a1][a2]amix=inputs=3", "-map [aout]", "normalize=0", "volume=1/3", "aresample=48000:async=1:first_pts=0"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ffmpeg args %q missing %q", joined, want)
		}
	}
}

// Isolated pipeline tests replace media decoding; integration tests use the real verifier.
func fakeAudioVerifier(_ context.Context, _, input, output string, source, out media.Probe, merge bool) (media.AudioVerification, error) {
	data, err := os.ReadFile(output)
	if err != nil {
		return media.AudioVerification{}, err
	}
	digest := sha256.Sum256(data)
	sourceData, err := os.ReadFile(input)
	if err != nil {
		return media.AudioVerification{}, err
	}
	sourceDigest := sha256.Sum256(sourceData)
	sourceSamples := make([]int64, source.AudioCount())
	outputSamples := make([]int64, out.AudioCount())
	for i := range sourceSamples {
		sourceSamples[i] = 480000
	}
	for i := range outputSamples {
		outputSamples[i] = 480000
	}
	return media.AudioVerification{
		Version: media.AudioVerificationVersion, Algorithm: media.AudioVerificationAlgorithm,
		Complete: true, Merge: merge, SourceTracks: source.AudioCount(), OutputTracks: out.AudioCount(),
		SampleRate: 48000, WindowMilliseconds: 200, Windows: 50, ActiveWindows: 50,
		MinimumDurationSeconds: 10, MinimumCorrelation: 1, SourceSamples: sourceSamples, OutputSamples: outputSamples,
		OutputSHA256: hex.EncodeToString(digest[:]), SourceSHA256: hex.EncodeToString(sourceDigest[:]),
	}, nil
}

func TestPipelineAudioLossKeepsSourceBeforePublicationAndTranscription(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "lost-audio.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0600); err != nil {
		t.Fatal(err)
	}
	p := successfulPipeline(cfg, source, func(string) error { t.Fatal("source deleted despite failed audio comparison"); return nil })
	p.VerifyAudio = func(context.Context, string, string, string, media.Probe, media.Probe, bool) (media.AudioVerification, error) {
		return media.AudioVerification{}, errors.New("output silent after 1950s")
	}
	p.Transcriber = transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
		t.Fatal("ASR ran before audio gate passed")
		return asr.Result{}, nil
	})
	_, err := p.Process(context.Background(), source, policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve})
	if err == nil || !strings.Contains(err.Error(), "audio preservation failed") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source not kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "lost-audio")); !os.IsNotExist(err) {
		t.Fatalf("bad output published: %v", err)
	}
}

func TestPipelineRefusesDeletionWhenPublishedMediaChangesWithoutProbeChange(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "source-present", true: "source-absent"}[absent], func(t *testing.T) {
			dir := t.TempDir()
			cfg := testConfig(dir)
			source := filepath.Join(cfg.AllowedInputDir, "changed-output.mp4")
			if err := os.MkdirAll(cfg.AllowedInputDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, make([]byte, 1000), 0600); err != nil {
				t.Fatal(err)
			}
			p := successfulPipeline(cfg, source, func(string) error { return errors.New("delete denied") })
			opts := policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve}
			if _, err := p.Process(context.Background(), source, opts); err == nil {
				t.Fatal("expected delete failure")
			}
			video := filepath.Join(cfg.OutputDir, "changed-output", "recording.mp4")
			changed := make([]byte, 500)
			changed[3] = 1
			if err := os.WriteFile(video, changed, 0600); err != nil {
				t.Fatal(err)
			}
			if absent {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
			}
			p.Remove = func(string) error { t.Fatal("deletion attempted for unverified bytes"); return nil }
			if _, err := p.Process(context.Background(), source, opts); err == nil || !strings.Contains(err.Error(), "audio preservation proof") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestPipelineLegacyManifestCannotAuthorizeDeletion(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "legacy.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0600); err != nil {
		t.Fatal(err)
	}
	p := successfulPipeline(cfg, source, func(string) error { return errors.New("delete denied") })
	opts := policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve}
	if _, err := p.Process(context.Background(), source, opts); err == nil {
		t.Fatal("expected delete failure")
	}
	path := filepath.Join(cfg.OutputDir, "legacy", "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]any
	if err := json.Unmarshal(data, &old); err != nil {
		t.Fatal(err)
	}
	old["version"] = 3
	delete(old, "audio_verification")
	data, err = json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	p.Remove = func(string) error { t.Fatal("legacy manifest authorized deletion"); return nil }
	if _, err := p.Process(context.Background(), source, opts); err == nil || !strings.Contains(err.Error(), "manifest version") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source not kept: %v", err)
	}
}

func TestPipelineRefusesDeletionForChangedSourceBytesWithSameIdentity(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "changed-source-content.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0600); err != nil {
		t.Fatal(err)
	}
	p := successfulPipeline(cfg, source, func(string) error { return errors.New("delete denied") })
	opts := policy.Options{DeleteSource: true, AudioMode: policy.AudioPreserve}
	if _, err := p.Process(context.Background(), source, opts); err == nil {
		t.Fatal("expected deletion failure")
	}
	before, err := inspectSourceIdentity(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	changed := make([]byte, 1000)
	changed[7] = 1
	if err := os.WriteFile(source, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := inspectSourceIdentity(source)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("fixture must preserve identity, size and mtime")
	}
	p.Remove = func(string) error { t.Fatal("changed source was deleted"); return nil }
	if _, err := p.Process(context.Background(), source, opts); err == nil || !strings.Contains(err.Error(), "source content changed") {
		t.Fatalf("error=%v", err)
	}
	if got, err := os.ReadFile(source); err != nil || got[7] != 1 {
		t.Fatalf("changed source missing: %v", err)
	}
}

func TestPipelineRefusesPublicationWhenASRStageChangesOutput(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	source := filepath.Join(cfg.AllowedInputDir, "changed-during-asr.mp4")
	if err := os.MkdirAll(cfg.AllowedInputDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, make([]byte, 1000), 0600); err != nil {
		t.Fatal(err)
	}
	p := successfulPipeline(cfg, source, func(string) error { t.Fatal("unverified source deletion"); return nil })
	p.Transcriber = transcriberFunc(func(_ context.Context, _ string, work string) (asr.Result, error) {
		video := filepath.Join(filepath.Dir(work), "recording.mp4")
		changed := make([]byte, 500)
		changed[0] = 1
		if err := os.WriteFile(video, changed, 0600); err != nil {
			return asr.Result{}, err
		}
		return asr.Result{Text: "Тест.", SpeechDetected: true, MetalConfirmed: true}, nil
	})
	if _, err := p.Process(context.Background(), source, policy.Options{AudioMode: policy.AudioPreserve}); err == nil || !strings.Contains(err.Error(), "output changed during processing") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "changed-during-asr")); !os.IsNotExist(err) {
		t.Fatalf("unverified output published: %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source missing: %v", err)
	}
}

func TestFFmpegCommandSingleAudioStillSeparatesDemux(t *testing.T) {
	cfg := testConfig(t.TempDir())
	source := media.Probe{Streams: []media.Stream{
		{Index: 0, CodecType: "video", CodecName: "hevc", Width: 1512, Height: 982, AvgFrameRate: "30/1"},
		{Index: 1, CodecType: "audio", CodecName: "aac"},
	}, Format: media.Format{Duration: "10", Size: "2000000"}}
	for _, mode := range []string{policy.AudioPreserve, policy.AudioMerge} {
		for _, force := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/copy", true: "/transcode"}[force], func(t *testing.T) {
				compression, args, err := (FFmpegCompressor{Config: cfg}).command("input.mp4", "output.mp4", source, policy.Options{AudioMode: mode}, EncodeSettings{AudioBitrateKbps: 96, ForceVideoTranscode: force})
				if err != nil {
					t.Fatal(err)
				}
				joined := strings.Join(args, " ")
				if strings.Count(joined, "-i input.mp4") != 2 || !strings.Contains(joined, "-map 0:v:0") || !strings.Contains(joined, "-map 1:a:0") || strings.Contains(joined, "-filter_complex") {
					t.Fatalf("single audio shares demux or adds mix: %s", joined)
				}
				wantMode := "copy"
				if force {
					wantMode = "transcode"
				}
				if compression.VideoMode != wantMode || compression.OutputAudioTracks != 1 || compression.AudioMode != mode {
					t.Fatalf("compression=%+v", compression)
				}
			})
		}
	}
}
