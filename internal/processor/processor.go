package processor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/asr"
	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/media"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
)

const (
	manifestVersion              = 4
	compressionCheckpointVersion = 2
	minimumCompressionTimeout    = 2 * time.Minute
	minimumTranscriptionTimeout  = 3 * time.Minute
)

type Compressor interface {
	Compress(context.Context, string, string, media.Probe, policy.Options, EncodeSettings) (Compression, error)
}

// EncodeSettings selects one bounded encoding attempt.
type EncodeSettings struct {
	AudioBitrateKbps    int
	ForceVideoTranscode bool
}

type CompressionAttempt struct {
	AudioBitrateKbps int    `json:"audio_bitrate_kbps"`
	VideoMode        string `json:"video_mode"`
	Error            string `json:"error"`
}

type Compression struct {
	FailedAttempts    []CompressionAttempt `json:"failed_attempts,omitempty"`
	VideoMode         string               `json:"video_mode"`
	VideoFilters      []string             `json:"video_filters,omitempty"`
	VideoBitrateKbps  int                  `json:"video_bitrate_kbps,omitempty"`
	AudioMode         string               `json:"audio_mode"`
	SourceAudioTracks int                  `json:"source_audio_tracks"`
	OutputAudioTracks int                  `json:"output_audio_tracks"`
	AudioBitrateKbps  int                  `json:"audio_bitrate_kbps"`
}

type FFmpegCompressor struct {
	Config config.Config
}

func (c FFmpegCompressor) Compress(ctx context.Context, inputPath, outputPath string, source media.Probe, options policy.Options, settings EncodeSettings) (Compression, error) {
	compression, args, err := c.command(inputPath, outputPath, source, options, settings)
	if err != nil {
		return Compression{}, err
	}
	defer os.Remove(outputPath + ".progress")
	output, err := exec.CommandContext(ctx, c.Config.FFmpegCommand, args...).CombinedOutput()
	if err != nil {
		return Compression{}, fmt.Errorf("compress video: %w: %s", err, compact(output))
	}
	return compression, nil
}

func (c FFmpegCompressor) command(inputPath, outputPath string, source media.Probe, options policy.Options, settings EncodeSettings) (Compression, []string, error) {
	if err := options.Validate(); err != nil {
		return Compression{}, nil, err
	}
	if !allowedAudioBitrate(c.Config.AudioBitrateKbps, settings.AudioBitrateKbps) {
		return Compression{}, nil, fmt.Errorf("unsupported AAC bitrate %d", settings.AudioBitrateKbps)
	}
	video, ok := source.Video()
	if !ok {
		return Compression{}, nil, fmt.Errorf("source has no video stream")
	}
	if source.AudioCount() == 0 {
		return Compression{}, nil, fmt.Errorf("source has no audio stream")
	}
	filters := make([]string, 0, 2)
	if video.Width != c.Config.OutputWidth || video.Height != c.Config.OutputHeight {
		filters = append(filters, fmt.Sprintf("scale=%d:%d:flags=lanczos", c.Config.OutputWidth, c.Config.OutputHeight))
	}
	if !media.MatchesFrameRate(video, c.Config.OutputFPS) {
		filters = append(filters, fmt.Sprintf("fps=%d", c.Config.OutputFPS))
	}
	copyVideo := !settings.ForceVideoTranscode && video.CodecName == "hevc" && len(filters) == 0
	videoMode := "transcode"
	if copyVideo {
		videoMode = "copy"
	}
	compression := Compression{
		VideoMode:         videoMode,
		VideoFilters:      filters,
		AudioMode:         options.AudioMode,
		SourceAudioTracks: source.AudioCount(),
		OutputAudioTracks: source.AudioCount(),
		AudioBitrateKbps:  settings.AudioBitrateKbps,
	}
	if options.AudioMode == policy.AudioMerge {
		compression.OutputAudioTracks = 1
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", inputPath,
	}
	// Every audio decoder owns an independent demuxer. A shared demuxer can
	// stop AAC inputs early in both preserve and merge modes.
	for range source.AudioCount() {
		args = append(args, "-i", inputPath)
	}
	args = append(args, "-map", "0:v:0", "-map_metadata", "0", "-map_chapters", "0")
	if options.AudioMode == policy.AudioMerge && source.AudioCount() > 1 {
		inputs := make([]string, 0, source.AudioCount())
		preparation := make([]string, 0, source.AudioCount())
		for index := 0; index < source.AudioCount(); index++ {
			preparation = append(preparation, fmt.Sprintf("[%d:a:%d]aresample=48000:async=1:first_pts=0,aformat=channel_layouts=stereo[a%d]", index+1, index, index))
			inputs = append(inputs, fmt.Sprintf("[a%d]", index))
		}
		// A fixed gain keeps the remaining tracks at the same level when a
		// microphone track ends. Verification mixes decoded tracks independently.
		audioFilter := strings.Join(preparation, ";") + fmt.Sprintf(";%samix=inputs=%d:duration=longest:normalize=0,volume=1/%d[aout]", strings.Join(inputs, ""), source.AudioCount(), source.AudioCount())
		args = append(args, "-filter_complex", audioFilter, "-map", "[aout]")
	} else {
		for index := 0; index < source.AudioCount(); index++ {
			args = append(args, "-map", fmt.Sprintf("%d:a:%d", index+1, index))
		}
	}
	if len(filters) > 0 {
		args = append(args, "-vf", strings.Join(filters, ","))
	}
	if copyVideo {
		args = append(args, "-c:v", "copy", "-tag:v", "hvc1")
	} else {
		videoBitrateKbps, err := c.videoBitrateKbps(source, compression.OutputAudioTracks, settings.AudioBitrateKbps)
		if err != nil {
			return Compression{}, nil, err
		}
		compression.VideoBitrateKbps = videoBitrateKbps
		args = append(args,
			"-c:v", "hevc_videotoolbox", "-tag:v", "hvc1",
			"-b:v", fmt.Sprintf("%dk", videoBitrateKbps),
			"-maxrate", fmt.Sprintf("%dk", videoBitrateKbps),
			"-bufsize", fmt.Sprintf("%dk", videoBitrateKbps*2),
			// Offline encoding must retain frames even when the bitrate budget
			// is tight. Real-time VideoToolbox can silently discard them.
			"-realtime", "0", "-prio_speed", "1",
		)
	}
	args = append(args,
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", settings.AudioBitrateKbps),
		"-max_muxing_queue_size", "4096",
		"-movflags", "+faststart",
		"-stats_period", "1", "-progress", outputPath+".progress",
		outputPath,
	)
	return compression, args, nil
}

func (c FFmpegCompressor) videoBitrateKbps(source media.Probe, outputAudioTracks, audioBitrateKbps int) (int, error) {
	duration := source.DurationSeconds()
	if duration <= 0 || source.SizeBytes() <= 0 {
		return 0, fmt.Errorf("source size and duration are required for bounded compression")
	}
	// Reserve two percentage points for MP4 muxing variance so the configured
	// maximum is a practical upper bound instead of an optimistic media target.
	mediaPercent := c.Config.MaxOutputSizePercent - 2
	totalTargetKbps := int(float64(source.SizeBytes()*8) / duration / 1000 * float64(mediaPercent) / 100)
	videoTargetKbps := totalTargetKbps - outputAudioTracks*audioBitrateKbps
	if videoTargetKbps < 128 {
		return 0, fmt.Errorf("source bitrate is too low for bounded HEVC compression")
	}
	return videoTargetKbps, nil
}

type ProbeFunc func(context.Context, string, string) (media.Probe, error)
type RemoveFunc func(string) error
type AudioVerifyFunc func(context.Context, string, string, string, media.Probe, media.Probe, bool) (media.AudioVerification, error)

type Pipeline struct {
	Config      config.Config
	Transcriber asr.Transcriber
	Compressor  Compressor
	Probe       ProbeFunc
	Remove      RemoveFunc
	VerifyAudio AudioVerifyFunc
}

type Result struct {
	SourcePath        string                  `json:"source_path"`
	FinalDir          string                  `json:"final_dir"`
	VideoPath         string                  `json:"video_path"`
	TranscriptPath    string                  `json:"transcript_path"`
	ManifestPath      string                  `json:"manifest_path"`
	SourceBytes       int64                   `json:"source_bytes"`
	OutputBytes       int64                   `json:"output_bytes"`
	ASR               asr.Result              `json:"asr"`
	Compression       Compression             `json:"compression"`
	AudioVerification media.AudioVerification `json:"audio_verification"`
	Options           policy.Options          `json:"options"`
	CompletedAt       time.Time               `json:"completed_at"`
}

type manifest struct {
	Version           int                     `json:"version"`
	SourcePath        string                  `json:"source_path"`
	SourceIdentity    sourceIdentity          `json:"source_identity"`
	SourceProbe       media.Probe             `json:"source_probe"`
	OutputProbe       media.Probe             `json:"output_probe"`
	ASR               asr.Result              `json:"asr"`
	Compression       Compression             `json:"compression"`
	AudioVerification media.AudioVerification `json:"audio_verification"`
	Options           policy.Options          `json:"options"`
	CompletedAt       time.Time               `json:"completed_at"`
}

type sourceIdentity struct {
	Device             uint64 `json:"device"`
	Inode              uint64 `json:"inode"`
	SizeBytes          int64  `json:"size_bytes"`
	ModifiedAtUnixNano int64  `json:"modified_at_unix_nano"`
}

type compressionSettings struct {
	OutputWidth          int `json:"output_width"`
	OutputHeight         int `json:"output_height"`
	OutputFPS            int `json:"output_fps"`
	MaxOutputSizePercent int `json:"max_output_size_percent"`
	AudioBitrateKbps     int `json:"audio_bitrate_kbps"`
}

type compressionCheckpoint struct {
	Version           int                     `json:"version"`
	SourcePath        string                  `json:"source_path"`
	SourceIdentity    sourceIdentity          `json:"source_identity"`
	SourceProbe       media.Probe             `json:"source_probe"`
	OutputProbe       media.Probe             `json:"output_probe"`
	Compression       Compression             `json:"compression"`
	AudioVerification media.AudioVerification `json:"audio_verification"`
	Options           policy.Options          `json:"options"`
	Settings          compressionSettings     `json:"settings"`
}

func New(cfg config.Config) Pipeline {
	return Pipeline{
		Config:      cfg,
		Transcriber: asr.Runner{Config: cfg},
		Compressor:  FFmpegCompressor{Config: cfg},
		Probe:       media.Inspect,
		Remove:      os.Remove,
		VerifyAudio: media.VerifyAudio,
	}
}

func (p Pipeline) Process(ctx context.Context, inputPath string, options policy.Options) (Result, error) {
	if err := options.Validate(); err != nil {
		return Result{}, err
	}
	resolvedInput, err := p.resolveInputPath(inputPath)
	if err != nil {
		return Result{}, err
	}
	stem := strings.TrimSuffix(filepath.Base(resolvedInput), filepath.Ext(resolvedInput))
	if strings.TrimSpace(stem) == "" {
		return Result{}, fmt.Errorf("input filename has no stem")
	}
	finalDir := filepath.Join(p.Config.OutputDir, stem)
	finalVideo := filepath.Join(finalDir, "recording.mp4")
	finalTranscript := filepath.Join(finalDir, "transcript.md")
	finalManifest := filepath.Join(finalDir, "manifest.json")

	if _, err := os.Stat(finalDir); err == nil {
		return p.finishExisting(ctx, resolvedInput, finalDir, finalVideo, finalTranscript, finalManifest, options)
	} else if !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("inspect final directory: %w", err)
	}
	initialSourceIdentity, err := inspectSourceIdentity(resolvedInput)
	if err != nil {
		return Result{}, err
	}

	sourceProbe, err := p.Probe(ctx, p.Config.FFprobeCommand, resolvedInput)
	if err != nil {
		return Result{}, fmt.Errorf("probe source: %w", err)
	}
	if err := os.MkdirAll(p.Config.OutputDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create output root: %w", err)
	}
	tempDir := p.processingDir(resolvedInput)
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create processing directory: %w", err)
	}
	tempVideo := filepath.Join(tempDir, "recording.mp4")
	tempTranscript := filepath.Join(tempDir, "transcript.md")
	tempManifest := filepath.Join(tempDir, "manifest.json")
	tempCheckpoint := filepath.Join(tempDir, "compression-checkpoint.json")
	workDir := filepath.Join(tempDir, "work")
	published := false
	defer func() {
		_ = os.RemoveAll(workDir)
		if !published {
			_ = os.Remove(tempTranscript)
			_ = os.Remove(tempManifest)
		}
	}()

	compression, outputProbe, audioVerification, err := p.ensureCompression(
		ctx,
		resolvedInput,
		tempDir,
		tempVideo,
		tempCheckpoint,
		initialSourceIdentity,
		sourceProbe,
		options,
	)
	if err != nil {
		return Result{}, err
	}
	if err := os.RemoveAll(workDir); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("reset ASR work directory: %w", err)
	}
	asrTimeout := transcriptionTimeout(sourceProbe.DurationSeconds())
	asrCtx, cancelASR := context.WithTimeout(ctx, asrTimeout)
	asrResult, err := p.Transcriber.Transcribe(asrCtx, resolvedInput, workDir)
	asrContextErr := asrCtx.Err()
	cancelASR()
	if err != nil {
		if errors.Is(asrContextErr, context.DeadlineExceeded) {
			return Result{}, fmt.Errorf("transcribe exceeded %s safety timeout: %w", asrTimeout, err)
		}
		return Result{}, fmt.Errorf("transcribe: %w", err)
	}
	if err := writeTranscript(tempTranscript, stem, asrResult); err != nil {
		return Result{}, err
	}
	completedAt := time.Now()
	manifestValue := manifest{
		Version:           manifestVersion,
		SourcePath:        resolvedInput,
		SourceIdentity:    initialSourceIdentity,
		SourceProbe:       sourceProbe,
		OutputProbe:       outputProbe,
		ASR:               asrResult,
		Compression:       compression,
		AudioVerification: audioVerification,
		Options:           options,
		CompletedAt:       completedAt,
	}
	if err := writeJSON(tempManifest, manifestValue); err != nil {
		return Result{}, err
	}
	if err := os.RemoveAll(workDir); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("remove ASR work directory: %w", err)
	}
	if err := os.Remove(tempCheckpoint); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("remove compression checkpoint: %w", err)
	}
	if err := syncFile(tempVideo); err != nil {
		return Result{}, err
	}
	if err := syncFile(tempTranscript); err != nil {
		return Result{}, err
	}
	if err := syncFile(tempManifest); err != nil {
		return Result{}, err
	}
	if err := syncDir(tempDir); err != nil {
		return Result{}, err
	}
	if err := validateSourceIdentity(resolvedInput, initialSourceIdentity); err != nil {
		return Result{}, fmt.Errorf("source changed during processing: %w", err)
	}
	if err := audioVerification.ValidateSource(ctx, resolvedInput); err != nil {
		return Result{}, fmt.Errorf("source content changed during processing: %w", err)
	}
	if err := audioVerification.ValidateOutput(ctx, tempVideo); err != nil {
		return Result{}, fmt.Errorf("output changed during processing; source kept: %w", err)
	}
	if err := os.Rename(tempDir, finalDir); err != nil {
		return Result{}, fmt.Errorf("publish processed recording: %w", err)
	}
	published = true
	if err := syncDir(p.Config.OutputDir); err != nil {
		return Result{}, err
	}
	if options.DeleteSource {
		if err := audioVerification.ValidateOutput(ctx, finalVideo); err != nil {
			return Result{}, fmt.Errorf("outputs published but source kept: audio verification failed before deletion: %w", err)
		}
		if err := audioVerification.ValidateSource(ctx, resolvedInput); err != nil {
			return Result{}, fmt.Errorf("outputs published but source kept: source audio verification failed before deletion: %w", err)
		}
		if err := removeSourceIfUnchanged(resolvedInput, initialSourceIdentity, p.Remove); err != nil {
			return Result{}, fmt.Errorf("processed outputs published but source deletion failed: %w", err)
		}
	}
	return Result{
		SourcePath:        resolvedInput,
		FinalDir:          finalDir,
		VideoPath:         finalVideo,
		TranscriptPath:    finalTranscript,
		ManifestPath:      finalManifest,
		SourceBytes:       sourceProbe.SizeBytes(),
		OutputBytes:       outputProbe.SizeBytes(),
		ASR:               asrResult,
		Compression:       compression,
		AudioVerification: audioVerification,
		Options:           options,
		CompletedAt:       completedAt,
	}, nil
}

func (p Pipeline) processingDir(inputPath string) string {
	digest := sha256.Sum256([]byte(inputPath))
	return filepath.Join(p.Config.OutputDir, fmt.Sprintf(".processing-%x", digest[:8]))
}

func (p Pipeline) ensureCompression(
	ctx context.Context,
	inputPath string,
	tempDir string,
	videoPath string,
	checkpointPath string,
	sourceIdentity sourceIdentity,
	sourceProbe media.Probe,
	options policy.Options,
) (Compression, media.Probe, media.AudioVerification, error) {
	settings := p.compressionSettings()
	if checkpoint, ok := p.validCompressionCheckpoint(ctx, checkpointPath, videoPath, inputPath, sourceIdentity, sourceProbe, options, settings); ok {
		return checkpoint.Compression, checkpoint.OutputProbe, checkpoint.AudioVerification, nil
	}
	if err := os.RemoveAll(tempDir); err != nil {
		return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("reset stale processing directory: %w", err)
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("recreate processing directory: %w", err)
	}

	// All encoding, probing and PCM checks share one deadline across the ladder.
	timeout := compressionTimeout(sourceProbe.DurationSeconds())
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	compression, outputProbe, audioVerification, err := p.compressAttempts(attemptCtx, inputPath, videoPath, sourceIdentity, sourceProbe, options)
	if err != nil {
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("compression exceeded %s safety timeout: %w", timeout, err)
		}
		return Compression{}, media.Probe{}, media.AudioVerification{}, err
	}
	checkpoint := compressionCheckpoint{
		Version:           compressionCheckpointVersion,
		SourcePath:        inputPath,
		SourceIdentity:    sourceIdentity,
		SourceProbe:       sourceProbe,
		OutputProbe:       outputProbe,
		Compression:       compression,
		AudioVerification: audioVerification,
		Options:           options,
		Settings:          settings,
	}
	if err := syncFile(videoPath); err != nil {
		return Compression{}, media.Probe{}, media.AudioVerification{}, err
	}
	if err := writeJSON(checkpointPath, checkpoint); err != nil {
		return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("write compression checkpoint: %w", err)
	}
	if err := syncFile(checkpointPath); err != nil {
		return Compression{}, media.Probe{}, media.AudioVerification{}, err
	}
	if err := syncDir(tempDir); err != nil {
		return Compression{}, media.Probe{}, media.AudioVerification{}, err
	}
	return compression, outputProbe, audioVerification, nil
}

// audioBitrates starts at the configured target, then only raises it to known
// AAC targets. A saved proof never authorizes an arbitrary bitrate.
func audioBitrates(initial int) []int {
	rates := []int{initial}
	for _, rate := range []int{128, 160, 192, 256, 320} {
		if rate > initial {
			rates = append(rates, rate)
		}
	}
	return rates
}

func allowedAudioBitrate(initial, actual int) bool {
	if initial < 32 || initial > 320 {
		return false
	}
	for _, rate := range audioBitrates(initial) {
		if actual == rate {
			return true
		}
	}
	return false
}

func (p Pipeline) compressAttempts(ctx context.Context, inputPath, videoPath string, identity sourceIdentity, source media.Probe, options policy.Options) (Compression, media.Probe, media.AudioVerification, error) {
	if p.VerifyAudio == nil {
		return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("audio verifier is unavailable; source must be kept")
	}
	var failed []CompressionAttempt
	var lastErr error
	for _, rate := range audioBitrates(p.Config.AudioBitrateKbps) {
		settings := EncodeSettings{AudioBitrateKbps: rate}
		for mode := 0; mode < 2; mode++ {
			if err := ctx.Err(); err != nil {
				return Compression{}, media.Probe{}, media.AudioVerification{}, err
			}
			if err := validateSourceIdentity(inputPath, identity); err != nil {
				return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("source changed during processing: %w", err)
			}
			compression, err := p.Compressor.Compress(ctx, inputPath, videoPath, source, options, settings)
			if err != nil {
				return Compression{}, media.Probe{}, media.AudioVerification{}, err
			}
			if err := ctx.Err(); err != nil {
				return Compression{}, media.Probe{}, media.AudioVerification{}, err
			}
			expectedTracks := source.AudioCount()
			if options.AudioMode == policy.AudioMerge {
				expectedTracks = 1
			}
			if compression.AudioBitrateKbps != rate || compression.AudioMode != options.AudioMode || compression.SourceAudioTracks != source.AudioCount() || compression.OutputAudioTracks != expectedTracks ||
				(compression.VideoMode != "copy" && compression.VideoMode != "transcode") || (settings.ForceVideoTranscode && compression.VideoMode != "transcode") {
				return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("compressor returned unexpected encoding settings; source kept")
			}
			output, err := p.Probe(ctx, p.Config.FFprobeCommand, videoPath)
			if err != nil {
				return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("probe compressed output: %w", err)
			}
			// An oversize copy can use the same AAC target with a bounded video budget.
			if err := media.ValidateSizeLimit(source, output, p.Config.MaxOutputSizePercent); err != nil {
				if compression.VideoMode == "copy" && !settings.ForceVideoTranscode {
					failed = append(failed, CompressionAttempt{rate, compression.VideoMode, err.Error()})
					settings.ForceVideoTranscode = true
					continue
				}
				return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("validate compressed output: %w", err)
			}
			if err := media.ValidateCompressed(source, output, p.Config.OutputWidth, p.Config.OutputHeight, p.Config.OutputFPS, rate, compression.OutputAudioTracks); err != nil {
				return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("validate compressed output: %w", err)
			}
			verification, err := p.VerifyAudio(ctx, p.Config.FFmpegCommand, inputPath, videoPath, source, output, options.AudioMode == policy.AudioMerge)
			if contextErr := ctx.Err(); contextErr != nil {
				return Compression{}, media.Probe{}, media.AudioVerification{}, contextErr
			}
			if err != nil {
				var qualityError *media.AudioQualityError
				if !errors.As(err, &qualityError) || (qualityError.Metric != "correlation" && qualityError.Metric != "energy") {
					return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("audio preservation failed; source kept: %w", err)
				}
				lastErr = err
				failed = append(failed, CompressionAttempt{rate, compression.VideoMode, err.Error()})
				break
			}
			if verification.SourceTracks != source.AudioCount() || verification.OutputTracks != output.AudioCount() || verification.Merge != (options.AudioMode == policy.AudioMerge) {
				return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("audio verification does not cover the selected tracks; source kept")
			}
			if err := verification.ValidateOutput(ctx, videoPath); err != nil {
				return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("invalid audio verification; source kept: %w", err)
			}
			if err := validateSourceIdentity(inputPath, identity); err != nil {
				return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("source changed during processing: %w", err)
			}
			if err := verification.ValidateSource(ctx, inputPath); err != nil {
				return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("invalid source audio verification; source kept: %w", err)
			}
			compression.FailedAttempts = failed
			return compression, output, verification, nil
		}
	}
	trace := make([]string, 0, len(failed))
	for _, attempt := range failed {
		trace = append(trace, fmt.Sprintf("%dk/%s: %s", attempt.AudioBitrateKbps, attempt.VideoMode, attempt.Error))
	}
	return Compression{}, media.Probe{}, media.AudioVerification{}, fmt.Errorf("audio preservation failed after %d bounded attempts; source kept; attempt trace [%s]: %w", len(failed), strings.Join(trace, "; "), lastErr)
}

func (p Pipeline) validCompressionCheckpoint(
	ctx context.Context,
	checkpointPath string,
	videoPath string,
	inputPath string,
	sourceIdentity sourceIdentity,
	sourceProbe media.Probe,
	options policy.Options,
	settings compressionSettings,
) (compressionCheckpoint, bool) {
	payload, err := os.ReadFile(checkpointPath)
	if err != nil {
		return compressionCheckpoint{}, false
	}
	var checkpoint compressionCheckpoint
	if json.Unmarshal(payload, &checkpoint) != nil ||
		checkpoint.Version != compressionCheckpointVersion ||
		checkpoint.SourcePath != inputPath ||
		checkpoint.SourceIdentity != sourceIdentity ||
		!reflect.DeepEqual(checkpoint.SourceProbe, sourceProbe) ||
		checkpoint.Options != options ||
		checkpoint.Settings != settings {
		return compressionCheckpoint{}, false
	}
	expectedAudioTracks := sourceProbe.AudioCount()
	if options.AudioMode == policy.AudioMerge {
		expectedAudioTracks = 1
	}
	if checkpoint.Compression.AudioMode != options.AudioMode ||
		checkpoint.Compression.SourceAudioTracks != sourceProbe.AudioCount() ||
		checkpoint.Compression.OutputAudioTracks != expectedAudioTracks ||
		!allowedAudioBitrate(settings.AudioBitrateKbps, checkpoint.Compression.AudioBitrateKbps) {
		return compressionCheckpoint{}, false
	}
	if checkpoint.AudioVerification.SourceTracks != sourceProbe.AudioCount() || checkpoint.AudioVerification.OutputTracks != expectedAudioTracks || checkpoint.AudioVerification.Merge != (options.AudioMode == policy.AudioMerge) {
		return compressionCheckpoint{}, false
	}
	actualOutput, err := p.Probe(ctx, p.Config.FFprobeCommand, videoPath)
	if err != nil || !reflect.DeepEqual(actualOutput, checkpoint.OutputProbe) {
		return compressionCheckpoint{}, false
	}
	if err := media.ValidateCompressed(sourceProbe, actualOutput, p.Config.OutputWidth, p.Config.OutputHeight, p.Config.OutputFPS, checkpoint.Compression.AudioBitrateKbps, checkpoint.Compression.OutputAudioTracks); err != nil {
		return compressionCheckpoint{}, false
	}
	if err := media.ValidateSizeLimit(sourceProbe, actualOutput, p.Config.MaxOutputSizePercent); err != nil {
		return compressionCheckpoint{}, false
	}
	if err := checkpoint.AudioVerification.ValidateOutput(ctx, videoPath); err != nil {
		return compressionCheckpoint{}, false
	}
	if err := checkpoint.AudioVerification.ValidateSource(ctx, inputPath); err != nil {
		return compressionCheckpoint{}, false
	}
	return checkpoint, true
}

func (p Pipeline) compressionSettings() compressionSettings {
	return compressionSettings{
		OutputWidth:          p.Config.OutputWidth,
		OutputHeight:         p.Config.OutputHeight,
		OutputFPS:            p.Config.OutputFPS,
		MaxOutputSizePercent: p.Config.MaxOutputSizePercent,
		AudioBitrateKbps:     p.Config.AudioBitrateKbps,
	}
}

func compressionTimeout(durationSeconds float64) time.Duration {
	// Offline HEVC conversion and complete PCM verification share this budget.
	// Requiring their combined throughput to exceed 4x rejects valid Retina
	// recordings on a busy Mac before the verification stage can finish.
	return stageTimeout(durationSeconds, 2, minimumCompressionTimeout)
}

func transcriptionTimeout(durationSeconds float64) time.Duration {
	return stageTimeout(durationSeconds, 5, minimumTranscriptionTimeout)
}

func stageTimeout(durationSeconds, realtimeDivisor float64, minimum time.Duration) time.Duration {
	if durationSeconds <= 0 {
		return minimum
	}
	timeout := time.Duration(durationSeconds / realtimeDivisor * float64(time.Second))
	if timeout < minimum {
		return minimum
	}
	return timeout
}

func (p Pipeline) finishExisting(ctx context.Context, inputPath, finalDir, videoPath, transcriptPath, manifestPath string, options policy.Options) (Result, error) {
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		return Result{}, fmt.Errorf("final directory already exists without readable manifest: %w", err)
	}
	var saved manifest
	if err := json.Unmarshal(payload, &saved); err != nil {
		return Result{}, fmt.Errorf("decode existing manifest: %w", err)
	}
	if saved.Version != manifestVersion {
		return Result{}, fmt.Errorf("existing manifest version is %d, want %d", saved.Version, manifestVersion)
	}
	if saved.SourcePath != inputPath {
		return Result{}, fmt.Errorf("final directory belongs to different source %s", saved.SourcePath)
	}
	if saved.Options != options {
		return Result{}, fmt.Errorf("existing result belongs to different processing options")
	}
	expectedAudioTracks := saved.SourceProbe.AudioCount()
	if options.AudioMode == policy.AudioMerge {
		expectedAudioTracks = 1
	}
	if saved.Compression.AudioMode != options.AudioMode ||
		saved.Compression.SourceAudioTracks != saved.SourceProbe.AudioCount() ||
		saved.Compression.OutputAudioTracks != expectedAudioTracks ||
		!allowedAudioBitrate(p.Config.AudioBitrateKbps, saved.Compression.AudioBitrateKbps) {
		return Result{}, fmt.Errorf("existing compression settings are invalid")
	}
	if err := validateTranscript(transcriptPath, filepath.Base(finalDir), saved.ASR); err != nil {
		return Result{}, err
	}
	currentOutput, err := p.Probe(ctx, p.Config.FFprobeCommand, videoPath)
	if err != nil {
		return Result{}, fmt.Errorf("probe existing compressed output: %w", err)
	}
	if !reflect.DeepEqual(currentOutput, saved.OutputProbe) {
		return Result{}, fmt.Errorf("existing output media probe differs from its manifest")
	}
	if saved.AudioVerification.SourceTracks != saved.SourceProbe.AudioCount() || saved.AudioVerification.OutputTracks != currentOutput.AudioCount() || saved.AudioVerification.Merge != (options.AudioMode == policy.AudioMerge) {
		return Result{}, fmt.Errorf("existing audio verification does not cover the selected tracks; source kept")
	}
	if err := saved.AudioVerification.ValidateOutput(ctx, videoPath); err != nil {
		return Result{}, fmt.Errorf("existing output lacks valid audio preservation proof; source kept: %w", err)
	}
	currentIdentity, identityErr := inspectSourceIdentity(inputPath)
	if errors.Is(identityErr, os.ErrNotExist) {
		if !options.DeleteSource {
			return Result{}, fmt.Errorf("source is unavailable for a keep-source result: %w", identityErr)
		}
		if err := media.ValidateCompressed(saved.SourceProbe, currentOutput, p.Config.OutputWidth, p.Config.OutputHeight, p.Config.OutputFPS, saved.Compression.AudioBitrateKbps, saved.Compression.OutputAudioTracks); err != nil {
			return Result{}, fmt.Errorf("validate existing compressed output after source deletion: %w", err)
		}
		if err := media.ValidateSizeLimit(saved.SourceProbe, currentOutput, p.Config.MaxOutputSizePercent); err != nil {
			return Result{}, fmt.Errorf("validate existing compressed output after source deletion: %w", err)
		}
		return resultFromManifest(inputPath, finalDir, videoPath, transcriptPath, manifestPath, saved, currentOutput), nil
	}
	if identityErr != nil {
		return Result{}, identityErr
	}
	if currentIdentity != saved.SourceIdentity {
		return Result{}, fmt.Errorf("source identity changed since the published result")
	}
	currentSource, err := p.Probe(ctx, p.Config.FFprobeCommand, inputPath)
	if err != nil {
		return Result{}, fmt.Errorf("probe source before retry deletion: %w", err)
	}
	if err := media.ValidateCompressed(currentSource, currentOutput, p.Config.OutputWidth, p.Config.OutputHeight, p.Config.OutputFPS, saved.Compression.AudioBitrateKbps, saved.Compression.OutputAudioTracks); err != nil {
		return Result{}, fmt.Errorf("validate existing compressed output: %w", err)
	}
	if err := media.ValidateSizeLimit(currentSource, currentOutput, p.Config.MaxOutputSizePercent); err != nil {
		return Result{}, fmt.Errorf("validate existing compressed output: %w", err)
	}
	if options.DeleteSource {
		if err := saved.AudioVerification.ValidateSource(ctx, inputPath); err != nil {
			return Result{}, fmt.Errorf("source content changed since audio verification; source kept: %w", err)
		}
		if err := removeSourceIfUnchanged(inputPath, saved.SourceIdentity, p.Remove); err != nil {
			return Result{}, fmt.Errorf("delete source after existing output validation: %w", err)
		}
	}
	return resultFromManifest(inputPath, finalDir, videoPath, transcriptPath, manifestPath, saved, currentOutput), nil
}

func resultFromManifest(inputPath, finalDir, videoPath, transcriptPath, manifestPath string, saved manifest, output media.Probe) Result {
	return Result{
		SourcePath:        inputPath,
		FinalDir:          finalDir,
		VideoPath:         videoPath,
		TranscriptPath:    transcriptPath,
		ManifestPath:      manifestPath,
		SourceBytes:       saved.SourceProbe.SizeBytes(),
		OutputBytes:       output.SizeBytes(),
		ASR:               saved.ASR,
		Compression:       saved.Compression,
		AudioVerification: saved.AudioVerification,
		Options:           saved.Options,
		CompletedAt:       saved.CompletedAt,
	}
}

func inspectSourceIdentity(path string) (sourceIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return sourceIdentity{}, fmt.Errorf("stat source identity: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return sourceIdentity{}, fmt.Errorf("source identity requires a non-empty regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return sourceIdentity{}, fmt.Errorf("source identity is unavailable")
	}
	return sourceIdentity{
		Device:             uint64(stat.Dev),
		Inode:              uint64(stat.Ino),
		SizeBytes:          info.Size(),
		ModifiedAtUnixNano: info.ModTime().UnixNano(),
	}, nil
}

func validateSourceIdentity(path string, expected sourceIdentity) error {
	actual, err := inspectSourceIdentity(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("source identity changed")
	}
	return nil
}

func removeSourceIfUnchanged(path string, expected sourceIdentity, remove RemoveFunc) error {
	if err := validateSourceIdentity(path, expected); err != nil {
		return fmt.Errorf("refuse to delete source: %w", err)
	}
	return remove(path)
}

func (p Pipeline) resolveInputPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve input path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve input symlinks: %w", err)
		}
		resolvedParent, parentErr := filepath.EvalSymlinks(filepath.Dir(absPath))
		if parentErr != nil {
			return "", fmt.Errorf("resolve missing input parent: %w", parentErr)
		}
		resolved = filepath.Join(resolvedParent, filepath.Base(absPath))
	}
	allowed, err := filepath.EvalSymlinks(p.Config.AllowedInputDir)
	if err != nil {
		return "", fmt.Errorf("resolve allowed input directory: %w", err)
	}
	relative, err := filepath.Rel(allowed, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("input %s is outside allowed directory %s", resolved, allowed)
	}
	switch strings.ToLower(filepath.Ext(resolved)) {
	case ".mp4", ".mov", ".mkv":
	default:
		return "", fmt.Errorf("unsupported recording extension %q", filepath.Ext(resolved))
	}
	return resolved, nil
}

func writeTranscript(path, title string, result asr.Result) error {
	payload, err := transcriptPayload(title, result)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	return nil
}

func validateTranscript(path, title string, result asr.Result) error {
	want, err := transcriptPayload(title, result)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read existing transcript: %w", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("existing transcript differs from its manifest")
	}
	return nil
}

func transcriptPayload(title string, result asr.Result) ([]byte, error) {
	text := strings.TrimSpace(result.Text)
	if !result.SpeechDetected {
		text = "_Речь не обнаружена._"
	} else if text == "" {
		return nil, fmt.Errorf("ASR reported speech but returned an empty transcript")
	}
	return []byte(fmt.Sprintf("# Расшифровка: %s\n\n%s\n", title, text)), nil
}

func writeJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("write JSON: %w", err)
	}
	return nil
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s for sync: %w", path, err)
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory %s for sync: %w", path, err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync directory %s: %w", path, err)
	}
	return nil
}

func compact(value []byte) string {
	text := strings.TrimSpace(strings.ReplaceAll(string(value), "\n", " "))
	if len(text) > 1000 {
		return text[len(text)-1000:]
	}
	return text
}
