package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCompressed(t *testing.T) {
	source := Probe{
		Streams: []Stream{
			{CodecType: "video", CodecName: "h264", Width: 3024, Height: 1964, AvgFrameRate: "60/1"},
			{CodecType: "audio", CodecName: "aac"},
			{CodecType: "audio", CodecName: "aac"},
			{CodecType: "audio", CodecName: "aac"},
		},
		Format: Format{Duration: "60.0", Size: "1000000"},
	}
	output := Probe{
		Streams: []Stream{
			{CodecType: "video", CodecName: "hevc", Width: 1512, Height: 982, AvgFrameRate: "30/1"},
			{CodecType: "audio", CodecName: "aac", BitRate: "96000"},
		},
		Format: Format{Duration: "59.98", Size: "600000"},
	}
	if err := ValidateCompressed(source, output, 1512, 982, 30, 96, 1); err != nil {
		t.Fatalf("ValidateCompressed() error = %v", err)
	}

	output.Streams = append(output.Streams, Stream{CodecType: "audio", CodecName: "aac", BitRate: "96000"})
	if err := ValidateCompressed(source, output, 1512, 982, 30, 96, 1); err == nil {
		t.Fatal("ValidateCompressed() accepted an extra audio stream")
	}
}

func TestValidateCompressedAcceptsLowerAverageAACBitrateForSilence(t *testing.T) {
	source := Probe{
		Streams: []Stream{{CodecType: "video"}, {CodecType: "audio"}},
		Format:  Format{Duration: "20", Size: "3000000"},
	}
	output := Probe{
		Streams: []Stream{
			{CodecType: "video", CodecName: "hevc", Width: 1512, Height: 982, AvgFrameRate: "30/1"},
			{CodecType: "audio", CodecName: "aac", BitRate: "12000"},
		},
		Format: Format{Duration: "20", Size: "2000000"},
	}
	if err := ValidateCompressed(source, output, 1512, 982, 30, 96, 1); err != nil {
		t.Fatalf("ValidateCompressed() error = %v", err)
	}
}

func TestValidateSizeLimit(t *testing.T) {
	source := Probe{Format: Format{Size: "1000"}}
	if err := ValidateSizeLimit(source, Probe{Format: Format{Size: "800"}}, 80); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSizeLimit(source, Probe{Format: Format{Size: "801"}}, 80); err == nil {
		t.Fatal("accepted output above configured size bound")
	}
}

func TestAudioEndSecondsOptionalMetadata(t *testing.T) {
	for _, test := range []struct {
		name, start, duration string
		end                   float64
		known                 bool
	}{
		{"zero-start", "0", "10", 10, true},
		{"delayed", "2.5", "10", 12.5, true},
		{"negative-start", "-0.25", "10", 9.75, true},
		{"zero-duration", "0", "0", 0, true},
		{"missing-duration", "0", "", 0, false},
		{"missing-start", "", "10", 0, false},
		{"mkv-unknown", "0", "N/A", 0, false},
		{"nan-duration", "0", "NaN", 0, false},
		{"infinite-start", "+Inf", "10", 0, false},
		{"negative-duration", "0", "-10", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			end, known := (Stream{StartTime: test.start, Duration: test.duration}).AudioEndSeconds()
			if known != test.known || (known && end != test.end) {
				t.Fatalf("end=%v known=%v; want %v %v", end, known, test.end, test.known)
			}
		})
	}
}

func TestValidateCompressedAudioStreamEnds(t *testing.T) {
	audio := func(start, duration string) Stream {
		return Stream{CodecType: "audio", CodecName: "aac", BitRate: "96000", StartTime: start, Duration: duration}
	}
	for _, test := range []struct {
		name           string
		source, output []Stream
		fail           bool
	}{
		{"preserve-each-track", []Stream{audio("0", "10"), audio("0", "6")}, []Stream{audio("0", "10"), audio("0", "6")}, false},
		{"preserve-lost-short-track-tail", []Stream{audio("0", "10"), audio("0", "6")}, []Stream{audio("0", "10"), audio("0", "5")}, true},
		{"merge-longest-track", []Stream{audio("0", "10"), audio("0", "6")}, []Stream{audio("0", "10")}, false},
		{"merge-truncated", []Stream{audio("0", "10"), audio("0", "6")}, []Stream{audio("0", "1")}, true},
		{"merge-too-long", []Stream{audio("0", "10"), audio("0", "6")}, []Stream{audio("0", "11")}, true},
		{"merge-pads-positive-start", []Stream{audio("2", "8"), audio("0", "6")}, []Stream{audio("0", "10")}, false},
		{"merge-missing-positive-start-padding", []Stream{audio("2", "8"), audio("0", "6")}, []Stream{audio("0", "8")}, true},
		{"merge-trims-negative-start", []Stream{audio("-1", "11"), audio("0", "6")}, []Stream{audio("0", "10")}, false},
		{"preserve-includes-start", []Stream{audio("2", "8")}, []Stream{audio("2", "8")}, false},
		{"output-includes-start", []Stream{audio("0", "10")}, []Stream{audio("2", "8")}, false},
		{"aac-tolerance", []Stream{audio("0", "10")}, []Stream{audio("0", "9.85")}, false},
		{"beyond-aac-tolerance", []Stream{audio("0", "10")}, []Stream{audio("0", "9.849")}, true},
		{"unknown-source-duration-needs-pcm", []Stream{audio("0", "N/A")}, []Stream{audio("0", "1")}, false},
		{"unknown-output-duration-needs-pcm", []Stream{audio("0", "10")}, []Stream{audio("0", "N/A")}, false},
		{"unknown-merge-longest-needs-pcm", []Stream{audio("0", "10"), audio("0", "N/A")}, []Stream{audio("0", "20")}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := Probe{Streams: append([]Stream{{CodecType: "video"}}, test.source...), Format: Format{Duration: "10", Size: "1000000"}}
			output := Probe{Streams: append([]Stream{{CodecType: "video", CodecName: "hevc", Width: 1512, Height: 982, AvgFrameRate: "30/1"}}, test.output...), Format: Format{Duration: "10", Size: "500000"}}
			err := ValidateCompressed(source, output, 1512, 982, 30, 96, len(test.output))
			if test.fail && (err == nil || !strings.Contains(err.Error(), "audio stream")) {
				t.Fatalf("truncated/misaligned audio accepted: %v", err)
			}
			if !test.fail && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInspectRejectsShortAudioWithFullVideoDuration(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	encoders, err := exec.Command(ffmpeg, "-hide_banner", "-encoders").CombinedOutput()
	if err != nil || !strings.Contains(string(encoders), "libx265") {
		t.Skip("libx265 unavailable")
	}
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.mp4")
	outputPath := filepath.Join(dir, "short-audio.mp4")
	encode := func(path, audioDuration string) {
		audioCommand(t, ffmpeg, "-f", "lavfi", "-i", "color=c=black:s=160x120:r=10:d=2", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration="+audioDuration,
			"-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "log-level=error", "-c:a", "aac", "-b:a", "96k", path)
	}
	encode(sourcePath, "2")
	encode(outputPath, "0.4")
	// Keep the fixture's source larger independently of encoder/container overhead.
	file, err := os.OpenFile(sourcePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(make([]byte, 65536))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("pad source: %v %v", writeErr, closeErr)
	}
	source, err := Inspect(context.Background(), ffprobe, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	output, err := Inspect(context.Background(), ffprobe, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if source.DurationSeconds() != output.DurationSeconds() {
		t.Fatalf("fixture needs matching full-video duration: %s vs %s", source.Format.Duration, output.Format.Duration)
	}
	sourceEnd, sourceKnown := source.Audios()[0].AudioEndSeconds()
	outputEnd, outputKnown := output.Audios()[0].AudioEndSeconds()
	if !sourceKnown || !outputKnown || sourceEnd < 1.9 || outputEnd > 0.5 {
		t.Fatalf("stream metadata missing or incorrect: source=%+v output=%+v", source.Audios(), output.Audios())
	}
	if err := ValidateCompressed(source, output, 160, 120, 10, 96, 1); err == nil || !strings.Contains(err.Error(), "audio stream 0 end") {
		t.Fatalf("short AAC passed full-video format duration: %v", err)
	}
}
