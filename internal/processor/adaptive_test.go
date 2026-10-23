package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/asr"
	"github.com/chupakobra6/obs-interview-pipeline/internal/media"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
)

// This fixture writes real disposable bytes and proofs; only codecs/PCM are
// substituted. Its probe reports each attempted bitrate, so a stale 96k
// validation limit cannot accidentally accept a successful 160k result.
type adaptiveFixture struct {
	p         Pipeline
	source    string
	calls     []EncodeSettings
	deadlines []time.Time
	rate      int
	size      int64
	asrCalls  int
}

func newAdaptiveFixture(t *testing.T) *adaptiveFixture {
	t.Helper()
	cfg := testConfig(t.TempDir())
	if err := os.MkdirAll(cfg.AllowedInputDir, 0700); err != nil {
		t.Fatal(err)
	}
	f := &adaptiveFixture{source: filepath.Join(cfg.AllowedInputDir, "adaptive.mp4"), size: 500}
	if err := os.WriteFile(f.source, make([]byte, 1000), 0600); err != nil {
		t.Fatal(err)
	}
	f.p = successfulPipeline(cfg, f.source, os.Remove)
	f.p.Compressor = compressorFunc(func(ctx context.Context, _, output string, source media.Probe, options policy.Options, settings EncodeSettings) (Compression, error) {
		f.calls = append(f.calls, settings)
		deadline, _ := ctx.Deadline()
		f.deadlines = append(f.deadlines, deadline)
		f.rate = settings.AudioBitrateKbps
		videoMode := "copy"
		if settings.ForceVideoTranscode {
			videoMode = "transcode"
		}
		tracks := source.AudioCount()
		if options.AudioMode == policy.AudioMerge {
			tracks = 1
		}
		return Compression{VideoMode: videoMode, AudioMode: options.AudioMode, SourceAudioTracks: source.AudioCount(), OutputAudioTracks: tracks, AudioBitrateKbps: f.rate}, os.WriteFile(output, make([]byte, f.size), 0600)
	})
	f.p.Probe = func(ctx context.Context, command, path string) (media.Probe, error) {
		probe, err := fakeProbe(f.source)(ctx, command, path)
		if filepath.Base(path) == "recording.mp4" {
			probe.Streams[1].BitRate = strconv.Itoa(f.rate * 1000)
			probe.Format.Size = strconv.FormatInt(f.size, 10)
		} else {
			probe.Streams = append(probe.Streams, media.Stream{CodecType: "audio", CodecName: "aac", BitRate: "160000"})
		}
		return probe, err
	}
	f.p.Transcriber = transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
		f.asrCalls++
		return asr.Result{Text: "Готово.", SpeechDetected: true, MetalConfirmed: true}, nil
	})
	return f
}

func (f *adaptiveFixture) qualityUntil(rate int) {
	f.p.VerifyAudio = func(ctx context.Context, command, input, output string, source, out media.Probe, merge bool) (media.AudioVerification, error) {
		if f.rate < rate {
			return media.AudioVerification{}, fmt.Errorf("PCM gate: %w", &media.AudioQualityError{AtSeconds: 246.6, Channel: 1, Metric: "correlation", Value: .843})
		}
		return fakeAudioVerifier(ctx, command, input, output, source, out, merge)
	}
}

func TestAdaptiveAudioRaisesRateBeforeASRPublishAndDelete(t *testing.T) {
	for _, initial := range []int{96, 128} {
		t.Run(strconv.Itoa(initial), func(t *testing.T) {
			f := newAdaptiveFixture(t)
			f.p.Config.AudioBitrateKbps = initial
			f.qualityUntil(160)
			result, err := f.p.Process(context.Background(), f.source, policy.Options{AudioMode: policy.AudioMerge, DeleteSource: true})
			if err != nil {
				t.Fatal(err)
			}
			want := []EncodeSettings{{AudioBitrateKbps: 96}, {AudioBitrateKbps: 128}, {AudioBitrateKbps: 160}}
			if initial == 128 {
				want = want[1:]
			}
			if !reflect.DeepEqual(f.calls, want) {
				t.Fatalf("attempts=%+v, want %+v", f.calls, want)
			}
			if result.Compression.AudioBitrateKbps != 160 || len(result.Compression.FailedAttempts) != len(want)-1 || f.asrCalls != 1 {
				t.Fatalf("result=%+v ASR=%d", result.Compression, f.asrCalls)
			}
			for _, deadline := range f.deadlines {
				if deadline.IsZero() || !deadline.Equal(f.deadlines[0]) {
					t.Fatalf("attempt deadline extended: %v", f.deadlines)
				}
			}
			if _, err := os.Stat(f.source); !os.IsNotExist(err) {
				t.Fatalf("source not deleted: %v", err)
			}
			if _, err := os.Stat(result.ManifestPath); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAdaptiveAudioStopsOnNonQualityFailures(t *testing.T) {
	for _, failure := range []string{"decode", "hash", "source-hash", "cancel", "deadline", "source-identity"} {
		t.Run(failure, func(t *testing.T) {
			f := newAdaptiveFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.p.VerifyAudio = func(ctx context.Context, command, input, output string, source, out media.Probe, merge bool) (media.AudioVerification, error) {
				switch failure {
				case "decode":
					return media.AudioVerification{}, errors.New("decode PCM: unexpected EOF")
				case "cancel":
					cancel()
					return media.AudioVerification{}, &media.AudioQualityError{Metric: "correlation", Value: .5}
				case "deadline":
					return media.AudioVerification{}, context.DeadlineExceeded
				case "source-identity":
					if err := os.WriteFile(input, make([]byte, 1200), 0600); err != nil {
						t.Fatal(err)
					}
					return media.AudioVerification{}, &media.AudioQualityError{Metric: "correlation", Value: .5}
				}
				proof, err := fakeAudioVerifier(ctx, command, input, output, source, out, merge)
				if failure == "hash" {
					proof.OutputSHA256 = strings.Repeat("0", 64)
				} else {
					proof.SourceSHA256 = strings.Repeat("0", 64)
				}
				return proof, err
			}
			if _, err := f.p.Process(ctx, f.source, policy.Options{AudioMode: policy.AudioMerge, DeleteSource: true}); err == nil {
				t.Fatal("unexpected success")
			}
			if len(f.calls) != 1 || f.asrCalls != 0 {
				t.Fatalf("compression=%d ASR=%d", len(f.calls), f.asrCalls)
			}
			if _, err := os.Stat(f.source); err != nil {
				t.Fatalf("source lost: %v", err)
			}
			if _, err := os.Stat(filepath.Join(f.p.Config.OutputDir, "adaptive")); !os.IsNotExist(err) {
				t.Fatalf("failed output published: %v", err)
			}
		})
	}
}

func TestAdaptiveAudioExhaustionKeepsSourceAndFailureReceipt(t *testing.T) {
	f := newAdaptiveFixture(t)
	f.qualityUntil(1000)
	if err := os.MkdirAll(f.p.Config.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(f.p.Config.StateDir, "previous-failure.json")
	if err := os.WriteFile(receipt, []byte("previous failure"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := f.p.Process(context.Background(), f.source, policy.Options{AudioMode: policy.AudioMerge, DeleteSource: true})
	if err == nil || !strings.Contains(err.Error(), "after 6 bounded attempts") {
		t.Fatalf("error=%v", err)
	}
	for _, rate := range []int{96, 128, 160, 192, 256, 320} {
		if !strings.Contains(err.Error(), fmt.Sprintf("%dk/copy: PCM gate:", rate)) {
			t.Fatalf("attempt trace omits rate %d: %v", rate, err)
		}
	}
	if len(f.calls) != 6 || f.asrCalls != 0 {
		t.Fatalf("compression=%d ASR=%d", len(f.calls), f.asrCalls)
	}
	if _, err := os.Stat(f.source); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(receipt); err != nil || string(data) != "previous failure" {
		t.Fatalf("failure receipt changed: %q %v", data, err)
	}
}

func TestAdaptiveAudioCopySizeFallbackUsesSameRate(t *testing.T) {
	f := newAdaptiveFixture(t)
	base := f.p.Compressor
	f.p.Compressor = compressorFunc(func(ctx context.Context, input, output string, source media.Probe, options policy.Options, settings EncodeSettings) (Compression, error) {
		f.size = 900
		if settings.ForceVideoTranscode {
			f.size = 500
		}
		return base.Compress(ctx, input, output, source, options, settings)
	})
	result, err := f.p.Process(context.Background(), f.source, policy.Options{AudioMode: policy.AudioMerge})
	if err != nil {
		t.Fatal(err)
	}
	want := []EncodeSettings{{AudioBitrateKbps: 96}, {AudioBitrateKbps: 96, ForceVideoTranscode: true}}
	if !reflect.DeepEqual(f.calls, want) || result.Compression.VideoMode != "transcode" || result.Compression.AudioBitrateKbps != 96 {
		t.Fatalf("attempts=%+v compression=%+v", f.calls, result.Compression)
	}
	if len(result.Compression.FailedAttempts) != 1 {
		t.Fatalf("failed attempts=%+v", result.Compression.FailedAttempts)
	}
}

func TestAdaptiveAudioActualRateCheckpointAndPublishedRetryAfterUnlink(t *testing.T) {
	f := newAdaptiveFixture(t)
	f.qualityUntil(160)
	f.p.Transcriber = transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
		f.asrCalls++
		return asr.Result{}, errors.New("ASR retry")
	})
	opts := policy.Options{AudioMode: policy.AudioMerge, DeleteSource: true}
	if _, err := f.p.Process(context.Background(), f.source, opts); err == nil || !strings.Contains(err.Error(), "ASR retry") {
		t.Fatalf("error=%v", err)
	}
	if len(f.calls) != 3 {
		t.Fatalf("attempts=%+v", f.calls)
	}
	f.p.VerifyAudio = func(context.Context, string, string, string, media.Probe, media.Probe, bool) (media.AudioVerification, error) {
		t.Fatal("validated checkpoint decoded again")
		return media.AudioVerification{}, nil
	}
	f.p.Transcriber = transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
		f.asrCalls++
		return asr.Result{Text: "Готово.", SpeechDetected: true, MetalConfirmed: true}, nil
	})
	f.p.Remove = func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return errors.New("crash after unlink")
	}
	if _, err := f.p.Process(context.Background(), f.source, opts); err == nil || !strings.Contains(err.Error(), "crash after unlink") {
		t.Fatalf("error=%v", err)
	}
	if len(f.calls) != 3 || f.asrCalls != 2 {
		t.Fatalf("compression=%d ASR=%d", len(f.calls), f.asrCalls)
	}
	result, err := f.p.Process(context.Background(), f.source, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compression.AudioBitrateKbps != 160 || len(f.calls) != 3 || f.asrCalls != 2 {
		t.Fatalf("compression=%+v calls=%d ASR=%d", result.Compression, len(f.calls), f.asrCalls)
	}
}

func TestAdaptiveAudioRejectsArbitrarySavedRate(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(strconv.FormatBool(published), func(t *testing.T) {
			f := newAdaptiveFixture(t)
			opts := policy.Options{AudioMode: policy.AudioMerge, DeleteSource: true}
			if published {
				f.p.Remove = func(string) error { return errors.New("keep source") }
			} else {
				f.p.Transcriber = transcriberFunc(func(context.Context, string, string) (asr.Result, error) {
					return asr.Result{}, errors.New("ASR retry")
				})
			}
			if _, err := f.p.Process(context.Background(), f.source, opts); err == nil {
				t.Fatal("expected injected failure")
			}
			resolved, err := f.p.resolveInputPath(f.source)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.p.processingDir(resolved), "compression-checkpoint.json")
			if published {
				path = filepath.Join(f.p.Config.OutputDir, "adaptive", "manifest.json")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var saved map[string]any
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			saved["compression"].(map[string]any)["audio_bitrate_kbps"] = 3200
			if err := writeJSON(path, saved); err != nil {
				t.Fatal(err)
			}
			f.p.Remove = func(string) error { t.Fatal("arbitrary rate authorized deletion"); return nil }
			f.p.Compressor = compressorFunc(func(context.Context, string, string, media.Probe, policy.Options, EncodeSettings) (Compression, error) {
				return Compression{}, errors.New("invalid checkpoint rebuilt")
			})
			_, err = f.p.Process(context.Background(), f.source, opts)
			if err == nil {
				t.Fatal("invalid saved rate accepted")
			}
			if published && !strings.Contains(err.Error(), "compression settings are invalid") {
				t.Fatalf("error=%v", err)
			}
			if !published && !strings.Contains(err.Error(), "invalid checkpoint rebuilt") {
				t.Fatalf("error=%v", err)
			}
			if _, err := os.Stat(f.source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFFmpegForceTranscodeUsesActualAudioBudget(t *testing.T) {
	cfg := testConfig(t.TempDir())
	source := media.Probe{Streams: []media.Stream{{CodecType: "video", CodecName: "hevc", Width: 1512, Height: 982, AvgFrameRate: "30/1"}, {CodecType: "audio", CodecName: "aac"}}, Format: media.Format{Duration: "10", Size: "2000000"}}
	compression, args, err := (FFmpegCompressor{Config: cfg}).command("input.mp4", "output.mp4", source, policy.Options{AudioMode: policy.AudioMerge}, EncodeSettings{AudioBitrateKbps: 160, ForceVideoTranscode: true})
	if err != nil {
		t.Fatal(err)
	}
	// 2,000,000 bytes / 10s = 1600k; 78% media target minus 160k audio.
	if compression.VideoMode != "transcode" || compression.VideoBitrateKbps != 1088 || compression.AudioBitrateKbps != 160 {
		t.Fatalf("compression=%+v", compression)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v hevc_videotoolbox") || !strings.Contains(joined, "-b:v 1088k") || !strings.Contains(joined, "-b:a 160k") {
		t.Fatalf("command=%s", joined)
	}
}

func TestAdaptiveAudioBoundsCopyFallbackAndQualityFailures(t *testing.T) {
	f := newAdaptiveFixture(t)
	base := f.p.Compressor
	f.p.Compressor = compressorFunc(func(ctx context.Context, input, output string, source media.Probe, options policy.Options, settings EncodeSettings) (Compression, error) {
		f.size = 1100 // Also exercise copy output larger than its source.
		if settings.ForceVideoTranscode {
			f.size = 500
		}
		return base.Compress(ctx, input, output, source, options, settings)
	})
	f.qualityUntil(1000)
	_, err := f.p.Process(context.Background(), f.source, policy.Options{AudioMode: policy.AudioMerge, DeleteSource: true})
	if err == nil || !strings.Contains(err.Error(), "after 12 bounded attempts") {
		t.Fatalf("error=%v", err)
	}
	if len(f.calls) != 12 || f.asrCalls != 0 {
		t.Fatalf("compression=%d ASR=%d", len(f.calls), f.asrCalls)
	}
	for i := 0; i < len(f.calls); i += 2 {
		if f.calls[i].ForceVideoTranscode || !f.calls[i+1].ForceVideoTranscode || f.calls[i].AudioBitrateKbps != f.calls[i+1].AudioBitrateKbps {
			t.Fatalf("attempts=%+v", f.calls)
		}
	}
	if _, err := os.Stat(f.source); err != nil {
		t.Fatal(err)
	}
}

func TestAdaptiveAudioOversizeTranscodeStopsBeforePCMAndASR(t *testing.T) {
	f := newAdaptiveFixture(t)
	f.size = 900
	f.p.VerifyAudio = func(context.Context, string, string, string, media.Probe, media.Probe, bool) (media.AudioVerification, error) {
		t.Fatal("PCM verification ran on oversize transcode")
		return media.AudioVerification{}, nil
	}
	_, err := f.p.Process(context.Background(), f.source, policy.Options{AudioMode: policy.AudioMerge, DeleteSource: true})
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("error=%v", err)
	}
	if len(f.calls) != 2 || f.calls[1].AudioBitrateKbps != 96 || !f.calls[1].ForceVideoTranscode || f.asrCalls != 0 {
		t.Fatalf("attempts=%+v ASR=%d", f.calls, f.asrCalls)
	}
	if _, err := os.Stat(f.source); err != nil {
		t.Fatal(err)
	}
}

func TestAdaptiveAudioStreamEOFStopsBeforeQualityRetryAndASR(t *testing.T) {
	f := newAdaptiveFixture(t)
	probe := f.p.Probe
	f.p.Probe = func(ctx context.Context, command, path string) (media.Probe, error) {
		result, err := probe(ctx, command, path)
		for i := range result.Streams {
			if result.Streams[i].CodecType != "audio" {
				continue
			}
			result.Streams[i].StartTime = "0"
			result.Streams[i].Duration = "10"
			if filepath.Base(path) == "recording.mp4" {
				result.Streams[i].Duration = "1"
			}
		}
		return result, err
	}
	f.p.VerifyAudio = func(context.Context, string, string, string, media.Probe, media.Probe, bool) (media.AudioVerification, error) {
		t.Fatal("PCM gate ran despite known truncated stream")
		return media.AudioVerification{}, nil
	}
	_, err := f.p.Process(context.Background(), f.source, policy.Options{AudioMode: policy.AudioMerge, DeleteSource: true})
	if err == nil || !strings.Contains(err.Error(), "merged audio stream end") {
		t.Fatalf("error=%v", err)
	}
	if len(f.calls) != 1 || f.asrCalls != 0 {
		t.Fatalf("compression=%d ASR=%d", len(f.calls), f.asrCalls)
	}
	if _, err := os.Stat(f.source); err != nil {
		t.Fatal(err)
	}
}
