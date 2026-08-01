package media

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

type Stream struct {
	Index        int    `json:"index"`
	CodecName    string `json:"codec_name"`
	CodecType    string `json:"codec_type"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	AvgFrameRate string `json:"avg_frame_rate,omitempty"`
	BitRate      string `json:"bit_rate,omitempty"`
}

type Format struct {
	Duration string `json:"duration"`
	Size     string `json:"size"`
}

type Probe struct {
	Streams []Stream `json:"streams"`
	Format  Format   `json:"format"`
}

func Inspect(ctx context.Context, ffprobeCommand, path string) (Probe, error) {
	args := []string{
		"-v", "error",
		"-show_entries", "format=duration,size:stream=index,codec_type,codec_name,width,height,avg_frame_rate,bit_rate",
		"-of", "json",
		path,
	}
	output, err := exec.CommandContext(ctx, ffprobeCommand, args...).CombinedOutput()
	if err != nil {
		return Probe{}, fmt.Errorf("ffprobe %s: %w: %s", path, err, strings.TrimSpace(string(output)))
	}
	var result Probe
	if err := json.Unmarshal(output, &result); err != nil {
		return Probe{}, fmt.Errorf("decode ffprobe output: %w", err)
	}
	if len(result.Streams) == 0 {
		return Probe{}, fmt.Errorf("ffprobe found no streams in %s", path)
	}
	return result, nil
}

func (p Probe) Video() (Stream, bool) {
	for _, stream := range p.Streams {
		if stream.CodecType == "video" {
			return stream, true
		}
	}
	return Stream{}, false
}

func (p Probe) AudioCount() int {
	return len(p.Audios())
}

func (p Probe) Audios() []Stream {
	audios := make([]Stream, 0)
	for _, stream := range p.Streams {
		if stream.CodecType == "audio" {
			audios = append(audios, stream)
		}
	}
	return audios
}

func (p Probe) Audio() (Stream, bool) {
	for _, stream := range p.Streams {
		if stream.CodecType == "audio" {
			return stream, true
		}
	}
	return Stream{}, false
}

func (p Probe) DurationSeconds() float64 {
	value, _ := strconv.ParseFloat(p.Format.Duration, 64)
	return value
}

func (p Probe) SizeBytes() int64 {
	value, _ := strconv.ParseInt(p.Format.Size, 10, 64)
	return value
}

func FrameRate(value string) float64 {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		result, _ := strconv.ParseFloat(value, 64)
		return result
	}
	numerator, _ := strconv.ParseFloat(parts[0], 64)
	denominator, _ := strconv.ParseFloat(parts[1], 64)
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func MatchesFrameRate(video Stream, fps int) bool {
	return math.Abs(FrameRate(video.AvgFrameRate)-float64(fps)) <= 0.02
}

func ValidateCompressed(source, output Probe, width, height, fps, audioBitrateKbps, expectedAudioTracks int) error {
	video, ok := output.Video()
	if !ok {
		return fmt.Errorf("compressed output has no video stream")
	}
	if video.CodecName != "hevc" {
		return fmt.Errorf("compressed codec is %q, want hevc", video.CodecName)
	}
	if video.Width != width || video.Height != height {
		return fmt.Errorf("compressed dimensions are %dx%d, want %dx%d", video.Width, video.Height, width, height)
	}
	if actual := FrameRate(video.AvgFrameRate); !MatchesFrameRate(video, fps) {
		return fmt.Errorf("compressed frame rate is %.3f, want %d", actual, fps)
	}
	if source.AudioCount() == 0 {
		return fmt.Errorf("source has no audio stream")
	}
	if expectedAudioTracks <= 0 {
		return fmt.Errorf("expected audio stream count must be positive")
	}
	if output.AudioCount() != expectedAudioTracks {
		return fmt.Errorf("compressed audio stream count is %d, want %d", output.AudioCount(), expectedAudioTracks)
	}
	target := audioBitrateKbps * 1000
	maximum := target + target/4
	for index, audio := range output.Audios() {
		if audio.CodecName != "aac" {
			return fmt.Errorf("compressed audio stream %d codec is %q, want aac", index, audio.CodecName)
		}
		bitRate, err := strconv.Atoi(audio.BitRate)
		if err != nil || bitRate <= 0 {
			return fmt.Errorf("compressed audio stream %d bitrate is unavailable", index)
		}
		if bitRate > maximum {
			return fmt.Errorf("compressed audio stream %d bitrate is %d, want no more than approximately %d", index, bitRate, target)
		}
	}
	sourceDuration := source.DurationSeconds()
	outputDuration := output.DurationSeconds()
	tolerance := math.Max(1, sourceDuration*0.01)
	if sourceDuration <= 0 || outputDuration <= 0 || math.Abs(sourceDuration-outputDuration) > tolerance {
		return fmt.Errorf("compressed duration %.3fs differs from source %.3fs", outputDuration, sourceDuration)
	}
	if output.SizeBytes() <= 0 {
		return fmt.Errorf("compressed output is empty")
	}
	if source.SizeBytes() > 0 && output.SizeBytes() >= source.SizeBytes() {
		return fmt.Errorf("compressed output is not smaller: %d >= %d bytes", output.SizeBytes(), source.SizeBytes())
	}
	return nil
}
