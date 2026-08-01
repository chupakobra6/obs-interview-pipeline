package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/asr"
	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/media"
)

type Compressor interface {
	Compress(context.Context, string, string) error
}

type FFmpegCompressor struct {
	Config config.Config
}

func (c FFmpegCompressor) Compress(ctx context.Context, inputPath, outputPath string) error {
	filter := fmt.Sprintf("scale=%d:%d:flags=lanczos,fps=%d", c.Config.OutputWidth, c.Config.OutputHeight, c.Config.OutputFPS)
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", inputPath,
		"-map", "0:v:0", "-map", "0:a?",
		"-map_metadata", "0", "-map_chapters", "0",
		"-vf", filter,
		"-c:v", "hevc_videotoolbox", "-tag:v", "hvc1", "-q:v", fmt.Sprintf("%d", c.Config.VideoQuality),
		"-c:a", "copy",
		"-max_muxing_queue_size", "4096",
		"-movflags", "+faststart",
		outputPath,
	}
	output, err := exec.CommandContext(ctx, c.Config.FFmpegCommand, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("compress video: %w: %s", err, compact(output))
	}
	return nil
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
	SourcePath     string     `json:"source_path"`
	FinalDir       string     `json:"final_dir"`
	VideoPath      string     `json:"video_path"`
	TranscriptPath string     `json:"transcript_path"`
	ManifestPath   string     `json:"manifest_path"`
	SourceBytes    int64      `json:"source_bytes"`
	OutputBytes    int64      `json:"output_bytes"`
	ASR            asr.Result `json:"asr"`
	CompletedAt    time.Time  `json:"completed_at"`
}

type manifest struct {
	Version     int         `json:"version"`
	SourcePath  string      `json:"source_path"`
	SourceProbe media.Probe `json:"source_probe"`
	OutputProbe media.Probe `json:"output_probe"`
	ASR         asr.Result  `json:"asr"`
	CompletedAt time.Time   `json:"completed_at"`
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

func (p Pipeline) Process(ctx context.Context, inputPath string) (Result, error) {
	resolvedInput, err := p.validateInput(inputPath)
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
		return p.finishExisting(ctx, resolvedInput, finalDir, finalVideo, finalTranscript, finalManifest)
	} else if !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("inspect final directory: %w", err)
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
	compressDone := make(chan error, 1)
	go func() {
		result, runErr := p.Transcriber.Transcribe(workCtx, resolvedInput, workDir)
		asrDone <- asrOutcome{result: result, err: runErr}
	}()
	go func() { compressDone <- p.Compressor.Compress(workCtx, resolvedInput, tempVideo) }()

	var asrResult asr.Result
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
		case compressErr := <-compressDone:
			compressDone = nil
			if compressErr != nil && firstErr == nil {
				firstErr = compressErr
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
	if err := media.ValidateCompressed(sourceProbe, outputProbe, p.Config.OutputWidth, p.Config.OutputHeight, p.Config.OutputFPS); err != nil {
		return Result{}, fmt.Errorf("validate compressed output: %w", err)
	}
	if err := writeTranscript(tempTranscript, stem, asrResult); err != nil {
		return Result{}, err
	}
	completedAt := time.Now()
	manifestValue := manifest{
		Version:     1,
		SourcePath:  resolvedInput,
		SourceProbe: sourceProbe,
		OutputProbe: outputProbe,
		ASR:         asrResult,
		CompletedAt: completedAt,
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
	if err := os.Rename(tempDir, finalDir); err != nil {
		return Result{}, fmt.Errorf("publish processed recording: %w", err)
	}
	published = true
	if err := syncDir(p.Config.OutputDir); err != nil {
		return Result{}, err
	}
	if p.Config.DeleteSourceOnSuccess {
		if err := p.Remove(resolvedInput); err != nil {
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
		CompletedAt:    completedAt,
	}, nil
}

func (p Pipeline) finishExisting(ctx context.Context, inputPath, finalDir, videoPath, transcriptPath, manifestPath string) (Result, error) {
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		return Result{}, fmt.Errorf("final directory already exists without readable manifest: %w", err)
	}
	var saved manifest
	if err := json.Unmarshal(payload, &saved); err != nil {
		return Result{}, fmt.Errorf("decode existing manifest: %w", err)
	}
	if saved.SourcePath != inputPath {
		return Result{}, fmt.Errorf("final directory belongs to different source %s", saved.SourcePath)
	}
	if _, err := os.Stat(transcriptPath); err != nil {
		return Result{}, fmt.Errorf("existing transcript is unavailable: %w", err)
	}
	currentOutput, err := p.Probe(ctx, p.Config.FFprobeCommand, videoPath)
	if err != nil {
		return Result{}, fmt.Errorf("probe existing compressed output: %w", err)
	}
	currentSource, err := p.Probe(ctx, p.Config.FFprobeCommand, inputPath)
	if err != nil {
		return Result{}, fmt.Errorf("probe source before retry deletion: %w", err)
	}
	if err := media.ValidateCompressed(currentSource, currentOutput, p.Config.OutputWidth, p.Config.OutputHeight, p.Config.OutputFPS); err != nil {
		return Result{}, fmt.Errorf("validate existing compressed output: %w", err)
	}
	if p.Config.DeleteSourceOnSuccess {
		if err := p.Remove(inputPath); err != nil {
			return Result{}, fmt.Errorf("delete source after existing output validation: %w", err)
		}
	}
	return Result{
		SourcePath:     inputPath,
		FinalDir:       finalDir,
		VideoPath:      videoPath,
		TranscriptPath: transcriptPath,
		ManifestPath:   manifestPath,
		SourceBytes:    currentSource.SizeBytes(),
		OutputBytes:    currentOutput.SizeBytes(),
		ASR:            saved.ASR,
		CompletedAt:    saved.CompletedAt,
	}, nil
}

func (p Pipeline) validateInput(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve input path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve input symlinks: %w", err)
	}
	allowed, err := filepath.EvalSymlinks(p.Config.AllowedInputDir)
	if err != nil {
		return "", fmt.Errorf("resolve allowed input directory: %w", err)
	}
	relative, err := filepath.Rel(allowed, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("input %s is outside allowed directory %s", resolved, allowed)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat input: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", fmt.Errorf("input must be a non-empty regular file")
	}
	switch strings.ToLower(filepath.Ext(resolved)) {
	case ".mp4", ".mov", ".mkv":
	default:
		return "", fmt.Errorf("unsupported recording extension %q", filepath.Ext(resolved))
	}
	return resolved, nil
}

func writeTranscript(path, title string, result asr.Result) error {
	text := strings.TrimSpace(result.Text)
	if !result.SpeechDetected {
		text = "_Речь не обнаружена._"
	} else if text == "" {
		return fmt.Errorf("ASR reported speech but returned an empty transcript")
	}
	payload := fmt.Sprintf("# Расшифровка: %s\n\n%s\n", title, text)
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	return nil
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
