package processor

import (
	"bytes"
	"context"
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

const manifestVersion = 3

type Compressor interface {
	Compress(context.Context, string, string, media.Probe, policy.Options) (Compression, error)
}

type Compression struct {
	VideoMode         string   `json:"video_mode"`
	VideoFilters      []string `json:"video_filters,omitempty"`
	AudioMode         string   `json:"audio_mode"`
	SourceAudioTracks int      `json:"source_audio_tracks"`
	OutputAudioTracks int      `json:"output_audio_tracks"`
	AudioBitrateKbps  int      `json:"audio_bitrate_kbps"`
}

type FFmpegCompressor struct {
	Config config.Config
}

func (c FFmpegCompressor) Compress(ctx context.Context, inputPath, outputPath string, source media.Probe, options policy.Options) (Compression, error) {
	compression, args, err := c.command(inputPath, outputPath, source, options)
	if err != nil {
		return Compression{}, err
	}
	output, err := exec.CommandContext(ctx, c.Config.FFmpegCommand, args...).CombinedOutput()
	if err != nil {
		return Compression{}, fmt.Errorf("compress video: %w: %s", err, compact(output))
	}
	return compression, nil
}

func (c FFmpegCompressor) command(inputPath, outputPath string, source media.Probe, options policy.Options) (Compression, []string, error) {
	if err := options.Validate(); err != nil {
		return Compression{}, nil, err
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
	copyVideo := video.CodecName == "hevc" && len(filters) == 0
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
		AudioBitrateKbps:  c.Config.AudioBitrateKbps,
	}
	if options.AudioMode == policy.AudioMerge {
		compression.OutputAudioTracks = 1
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", inputPath,
		"-map", "0:v:0",
		"-map_metadata", "0", "-map_chapters", "0",
	}
	if options.AudioMode == policy.AudioMerge && source.AudioCount() > 1 {
		inputs := make([]string, 0, source.AudioCount())
		for index := 0; index < source.AudioCount(); index++ {
			inputs = append(inputs, fmt.Sprintf("[0:a:%d]", index))
		}
		audioFilter := fmt.Sprintf("%samix=inputs=%d:duration=longest:dropout_transition=0:normalize=1[aout]", strings.Join(inputs, ""), source.AudioCount())
		args = append(args, "-filter_complex", audioFilter, "-map", "[aout]")
	} else if options.AudioMode == policy.AudioMerge {
		args = append(args, "-map", "0:a:0")
	} else {
		args = append(args, "-map", "0:a")
	}
	if len(filters) > 0 {
		args = append(args, "-vf", strings.Join(filters, ","))
	}
	if copyVideo {
		args = append(args, "-c:v", "copy", "-tag:v", "hvc1")
	} else {
		args = append(args,
			"-c:v", "hevc_videotoolbox", "-tag:v", "hvc1", "-q:v", fmt.Sprintf("%d", c.Config.VideoQuality),
		)
	}
	args = append(args,
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", c.Config.AudioBitrateKbps),
		"-max_muxing_queue_size", "4096",
		"-movflags", "+faststart",
		outputPath,
	)
	return compression, args, nil
}

type ProbeFunc func(context.Context, string, string) (media.Probe, error)
type RemoveFunc func(string) error

type Pipeline struct {
	Config      config.Config
	Transcriber asr.Transcriber
	Compressor  Compressor
	Probe       ProbeFunc
	Remove      RemoveFunc
}

type Result struct {
	SourcePath     string         `json:"source_path"`
	FinalDir       string         `json:"final_dir"`
	VideoPath      string         `json:"video_path"`
	TranscriptPath string         `json:"transcript_path"`
	ManifestPath   string         `json:"manifest_path"`
	SourceBytes    int64          `json:"source_bytes"`
	OutputBytes    int64          `json:"output_bytes"`
	ASR            asr.Result     `json:"asr"`
	Compression    Compression    `json:"compression"`
	Options        policy.Options `json:"options"`
	CompletedAt    time.Time      `json:"completed_at"`
}

type manifest struct {
	Version        int            `json:"version"`
	SourcePath     string         `json:"source_path"`
	SourceIdentity sourceIdentity `json:"source_identity"`
	SourceProbe    media.Probe    `json:"source_probe"`
	OutputProbe    media.Probe    `json:"output_probe"`
	ASR            asr.Result     `json:"asr"`
	Compression    Compression    `json:"compression"`
	Options        policy.Options `json:"options"`
	CompletedAt    time.Time      `json:"completed_at"`
}

type sourceIdentity struct {
	Device             uint64 `json:"device"`
	Inode              uint64 `json:"inode"`
	SizeBytes          int64  `json:"size_bytes"`
	ModifiedAtUnixNano int64  `json:"modified_at_unix_nano"`
}

func New(cfg config.Config) Pipeline {
	return Pipeline{
		Config:      cfg,
		Transcriber: asr.Runner{Config: cfg},
		Compressor:  FFmpegCompressor{Config: cfg},
		Probe:       media.Inspect,
		Remove:      os.Remove,
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

	if err := os.MkdirAll(p.Config.OutputDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create output root: %w", err)
	}
	tempDir, err := os.MkdirTemp(p.Config.OutputDir, ".processing-")
	if err != nil {
		return Result{}, fmt.Errorf("create processing directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(tempDir)
		}
	}()
	tempVideo := filepath.Join(tempDir, "recording.mp4")
	tempTranscript := filepath.Join(tempDir, "transcript.md")
	tempManifest := filepath.Join(tempDir, "manifest.json")
	workDir := filepath.Join(tempDir, "work")

	sourceProbe, err := p.Probe(ctx, p.Config.FFprobeCommand, resolvedInput)
	if err != nil {
		return Result{}, fmt.Errorf("probe source: %w", err)
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type asrOutcome struct {
		result asr.Result
		err    error
	}
	asrDone := make(chan asrOutcome, 1)
	type compressOutcome struct {
		result Compression
		err    error
	}
	compressDone := make(chan compressOutcome, 1)
	go func() {
		result, runErr := p.Transcriber.Transcribe(workCtx, resolvedInput, workDir)
		asrDone <- asrOutcome{result: result, err: runErr}
	}()
	go func() {
		result, compressErr := p.Compressor.Compress(workCtx, resolvedInput, tempVideo, sourceProbe, options)
		compressDone <- compressOutcome{result: result, err: compressErr}
	}()

	var asrResult asr.Result
	var compression Compression
	var firstErr error
	for asrDone != nil || compressDone != nil {
		select {
		case outcome := <-asrDone:
			asrDone = nil
			asrResult = outcome.result
			if outcome.err != nil && firstErr == nil {
				firstErr = fmt.Errorf("transcribe: %w", outcome.err)
				cancel()
			}
		case outcome := <-compressDone:
			compressDone = nil
			compression = outcome.result
			if outcome.err != nil && firstErr == nil {
				firstErr = outcome.err
				cancel()
			}
		}
	}
	if firstErr != nil {
		return Result{}, firstErr
	}

	outputProbe, err := p.Probe(ctx, p.Config.FFprobeCommand, tempVideo)
	if err != nil {
		return Result{}, fmt.Errorf("probe compressed output: %w", err)
	}
	if err := media.ValidateCompressed(sourceProbe, outputProbe, p.Config.OutputWidth, p.Config.OutputHeight, p.Config.OutputFPS, p.Config.AudioBitrateKbps, compression.OutputAudioTracks); err != nil {
		return Result{}, fmt.Errorf("validate compressed output: %w", err)
	}
	if err := writeTranscript(tempTranscript, stem, asrResult); err != nil {
		return Result{}, err
	}
	completedAt := time.Now()
	manifestValue := manifest{
		Version:        manifestVersion,
		SourcePath:     resolvedInput,
		SourceIdentity: initialSourceIdentity,
		SourceProbe:    sourceProbe,
		OutputProbe:    outputProbe,
		ASR:            asrResult,
		Compression:    compression,
		Options:        options,
		CompletedAt:    completedAt,
	}
	if err := writeJSON(tempManifest, manifestValue); err != nil {
		return Result{}, err
	}
	if err := os.RemoveAll(workDir); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("remove ASR work directory: %w", err)
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
	if err := os.Rename(tempDir, finalDir); err != nil {
		return Result{}, fmt.Errorf("publish processed recording: %w", err)
	}
	published = true
	if err := syncDir(p.Config.OutputDir); err != nil {
		return Result{}, err
	}
	if options.DeleteSource {
		if err := removeSourceIfUnchanged(resolvedInput, initialSourceIdentity, p.Remove); err != nil {
			return Result{}, fmt.Errorf("processed outputs published but source deletion failed: %w", err)
		}
	}
	return Result{
		SourcePath:     resolvedInput,
		FinalDir:       finalDir,
		VideoPath:      finalVideo,
		TranscriptPath: finalTranscript,
		ManifestPath:   finalManifest,
		SourceBytes:    sourceProbe.SizeBytes(),
		OutputBytes:    outputProbe.SizeBytes(),
		ASR:            asrResult,
		Compression:    compression,
		Options:        options,
		CompletedAt:    completedAt,
	}, nil
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
	currentIdentity, identityErr := inspectSourceIdentity(inputPath)
	if errors.Is(identityErr, os.ErrNotExist) {
		if !options.DeleteSource {
			return Result{}, fmt.Errorf("source is unavailable for a keep-source result: %w", identityErr)
		}
		if err := media.ValidateCompressed(saved.SourceProbe, currentOutput, p.Config.OutputWidth, p.Config.OutputHeight, p.Config.OutputFPS, p.Config.AudioBitrateKbps, saved.Compression.OutputAudioTracks); err != nil {
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
	if err := media.ValidateCompressed(currentSource, currentOutput, p.Config.OutputWidth, p.Config.OutputHeight, p.Config.OutputFPS, p.Config.AudioBitrateKbps, saved.Compression.OutputAudioTracks); err != nil {
		return Result{}, fmt.Errorf("validate existing compressed output: %w", err)
	}
	if options.DeleteSource {
		if err := removeSourceIfUnchanged(inputPath, saved.SourceIdentity, p.Remove); err != nil {
			return Result{}, fmt.Errorf("delete source after existing output validation: %w", err)
		}
	}
	return resultFromManifest(inputPath, finalDir, videoPath, transcriptPath, manifestPath, saved, currentOutput), nil
}

func resultFromManifest(inputPath, finalDir, videoPath, transcriptPath, manifestPath string, saved manifest, output media.Probe) Result {
	return Result{
		SourcePath:     inputPath,
		FinalDir:       finalDir,
		VideoPath:      videoPath,
		TranscriptPath: transcriptPath,
		ManifestPath:   manifestPath,
		SourceBytes:    saved.SourceProbe.SizeBytes(),
		OutputBytes:    output.SizeBytes(),
		ASR:            saved.ASR,
		Compression:    saved.Compression,
		Options:        saved.Options,
		CompletedAt:    saved.CompletedAt,
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
