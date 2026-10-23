package media

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
)

const (
	AudioVerificationVersion     = 2
	AudioVerificationAlgorithm   = "pcm-48k-stereo-window-v2"
	audioRate                    = 48000
	audioWindowMilliseconds      = 200
	audioWindowFrames            = audioRate * audioWindowMilliseconds / 1000
	audioActiveRMS               = 0.00031622776601683794 // -70 dBFS.
	audioMinimumCorrelation      = 0.85
	audioDurationToleranceFrames = 7200 // AAC padding and resampling: 150 ms.
	maxAudioTracks               = 16
)

// AudioQualityError reports a decoded signal mismatch that a higher AAC bitrate
// may resolve. Duration, decode, identity and hash failures never use this type.
// A retry must still pass the same complete PCM comparison before publication.
type AudioQualityError struct {
	AtSeconds float64
	Channel   int
	Metric    string
	Value     float64
}

func (e *AudioQualityError) Error() string {
	if e.Metric == "energy" {
		return fmt.Sprintf("audio energy lost/changed at %.3fs channel %d (ratio %.3f)", e.AtSeconds, e.Channel, e.Value)
	}
	return fmt.Sprintf("audio content differs at %.3fs channel %d (correlation %.3f)", e.AtSeconds, e.Channel, e.Value)
}

// AudioVerification records a complete content comparison and binds it to the
// exact source and output bytes. Sample counts are stereo frames, not interleaved values.
type AudioVerification struct {
	Version                int     `json:"version"`
	Algorithm              string  `json:"algorithm"`
	Complete               bool    `json:"complete"`
	Merge                  bool    `json:"merge"`
	SourceTracks           int     `json:"source_tracks"`
	OutputTracks           int     `json:"output_tracks"`
	SampleRate             int     `json:"sample_rate"`
	WindowMilliseconds     int     `json:"window_milliseconds"`
	Windows                int     `json:"windows"`
	ActiveWindows          int     `json:"active_windows"`
	MinimumDurationSeconds float64 `json:"minimum_duration_seconds"`
	MinimumCorrelation     float64 `json:"minimum_correlation"`
	SourceSamples          []int64 `json:"source_samples"`
	OutputSamples          []int64 `json:"output_samples"`
	SourceSHA256           string  `json:"source_sha256"`
	OutputSHA256           string  `json:"output_sha256"`
}

// ValidateOutput rejects incomplete/obsolete evidence and changed output files.
func (v AudioVerification) ValidateOutput(ctx context.Context, path string) error {
	if err := v.validateProof(); err != nil {
		return err
	}
	digest, err := audioSHA256(ctx, path)
	if err != nil {
		return err
	}
	if digest != v.OutputSHA256 {
		return fmt.Errorf("audio verification output SHA256 mismatch")
	}
	return nil
}

// ValidateSource requires the exact source bytes used for content verification.
func (v AudioVerification) ValidateSource(ctx context.Context, path string) error {
	if err := v.validateProof(); err != nil {
		return err
	}
	digest, err := audioSHA256(ctx, path)
	if err != nil {
		return err
	}
	if digest != v.SourceSHA256 {
		return fmt.Errorf("audio verification source SHA256 mismatch")
	}
	return nil
}

func (v AudioVerification) validateProof() error {
	if v.Version != AudioVerificationVersion || v.Algorithm != AudioVerificationAlgorithm || !v.Complete || v.SampleRate != audioRate || v.WindowMilliseconds != audioWindowMilliseconds {
		return fmt.Errorf("missing, incomplete or unsupported audio verification")
	}
	if v.SourceTracks < 1 || v.SourceTracks > maxAudioTracks || v.OutputTracks < 1 || len(v.SourceSamples) != v.SourceTracks || len(v.OutputSamples) != v.OutputTracks || (v.Merge && v.OutputTracks != 1) || (!v.Merge && v.OutputTracks != v.SourceTracks) {
		return fmt.Errorf("invalid audio verification track coverage")
	}
	minFrames := int64(math.MaxInt64)
	for _, n := range v.SourceSamples {
		if n <= 0 {
			return fmt.Errorf("empty source audio decode")
		}
		minFrames = min(minFrames, n)
	}
	for _, n := range v.OutputSamples {
		if n <= 0 {
			return fmt.Errorf("empty output audio decode")
		}
	}
	expectedWindows := 0
	if v.Merge {
		longest := int64(0)
		for _, n := range v.SourceSamples {
			longest = max(longest, n)
		}
		if absAudioFrames(longest-v.OutputSamples[0]) > audioDurationToleranceFrames {
			return fmt.Errorf("incomplete merged audio duration")
		}
		expectedWindows = audioWindowCount(max(longest, v.OutputSamples[0]))
	} else {
		for i, n := range v.SourceSamples {
			if absAudioFrames(n-v.OutputSamples[i]) > audioDurationToleranceFrames {
				return fmt.Errorf("incomplete audio track %d duration", i)
			}
			expectedWindows += audioWindowCount(max(n, v.OutputSamples[i]))
		}
	}
	if v.Windows != expectedWindows || v.Windows < 1 || v.ActiveWindows < 0 || v.ActiveWindows > v.Windows || !finiteAudio(v.MinimumDurationSeconds) || math.Abs(v.MinimumDurationSeconds-float64(minFrames)/audioRate) > 1.0/audioRate || !finiteAudio(v.MinimumCorrelation) || v.MinimumCorrelation < audioMinimumCorrelation || v.MinimumCorrelation > 1 {
		return fmt.Errorf("invalid audio verification window/content coverage")
	}
	for _, value := range []string{v.SourceSHA256, v.OutputSHA256} {
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != sha256.Size || strings.ToLower(value) != value {
			return fmt.Errorf("invalid audio verification SHA256")
		}
	}
	return nil
}

// VerifyAudio decodes every input and output audio track to EOF. Merge references
// are summed here, independently of the encoder's mixing filter. Memory is
// bounded by 200 ms PCM blocks; keep uses two processes, merge at most 17.
func VerifyAudio(ctx context.Context, ffmpeg, input, output string, source, outputProbe Probe, merge bool) (AudioVerification, error) {
	v := AudioVerification{Version: AudioVerificationVersion, Algorithm: AudioVerificationAlgorithm, Merge: merge, SourceTracks: source.AudioCount(), OutputTracks: outputProbe.AudioCount(), SampleRate: audioRate, WindowMilliseconds: audioWindowMilliseconds, MinimumCorrelation: 1}
	if v.SourceTracks < 1 || v.SourceTracks > maxAudioTracks || (!merge && v.SourceTracks != v.OutputTracks) || (merge && v.OutputTracks != 1) {
		return v, fmt.Errorf("unsupported audio verification track counts")
	}
	for _, p := range []Probe{source, outputProbe} {
		for _, s := range p.Audios() {
			if s.Channels != 1 && s.Channels != 2 {
				return v, fmt.Errorf("audio stream %d has unsupported channel count %d", s.Index, s.Channels)
			}
		}
	}
	before, err := os.Stat(output)
	if err != nil {
		return v, err
	}
	if !before.Mode().IsRegular() {
		return v, fmt.Errorf("audio output is not a regular file")
	}
	v.SourceSHA256, err = audioSHA256(ctx, input)
	if err != nil {
		return v, err
	}
	v.OutputSHA256, err = audioSHA256(ctx, output)
	if err != nil {
		return v, err
	}
	v.SourceSamples = make([]int64, v.SourceTracks)
	v.OutputSamples = make([]int64, v.OutputTracks)
	if merge {
		err = verifyAudioGroup(ctx, ffmpeg, input, output, source.Audios(), outputProbe.Audios()[0], &v, 0, true)
	} else {
		for i, s := range source.Audios() {
			if err = verifyAudioGroup(ctx, ffmpeg, input, output, []Stream{s}, outputProbe.Audios()[i], &v, i, false); err != nil {
				break
			}
		}
	}
	comparisonErr := err
	minFrames := v.SourceSamples[0]
	for _, n := range v.SourceSamples {
		minFrames = min(minFrames, n)
	}
	v.MinimumDurationSeconds = float64(minFrames) / audioRate
	outputDigest, err := audioSHA256(ctx, output)
	if err != nil {
		return v, err
	}
	if outputDigest != v.OutputSHA256 {
		return v, fmt.Errorf("audio output changed during verification (SHA256 mismatch)")
	}
	after, err := os.Stat(output)
	if err != nil {
		return v, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return v, fmt.Errorf("audio output changed during verification")
	}
	sourceDigest, err := audioSHA256(ctx, input)
	if err != nil {
		return v, err
	}
	if sourceDigest != v.SourceSHA256 {
		return v, fmt.Errorf("audio source changed during verification (SHA256 mismatch)")
	}
	// A codec-quality retry must never hide source/output mutation. Check the
	// byte identities even when the signal comparison stopped at a mismatch.
	if comparisonErr != nil {
		return v, comparisonErr
	}
	// Matching decoders can both report an early successful EOF. Compare every
	// decoded timeline to its own declared stream end before certifying coverage.
	for _, group := range []struct {
		name    string
		streams []Stream
		samples []int64
	}{
		{"source", source.Audios(), v.SourceSamples},
		{"output", outputProbe.Audios(), v.OutputSamples},
	} {
		for i, stream := range group.streams {
			if err := validateDecodedAudioEnd(group.name, stream, group.samples[i]); err != nil {
				return v, err
			}
		}
	}
	v.Complete = true
	if err = v.validateProof(); err != nil {
		v.Complete = false
		return v, err
	}
	return v, nil
}

func validateDecodedAudioEnd(name string, stream Stream, frames int64) error {
	if end, known := stream.AudioEndSeconds(); known {
		expected := math.Max(0, end)
		decoded := float64(frames) / audioRate
		if math.Abs(decoded-expected) > float64(audioDurationToleranceFrames)/audioRate+1.0/audioRate {
			return fmt.Errorf("%s audio stream %d decoded end %.3fs differs from declared end %.3fs (150ms tolerance)", name, stream.Index, decoded, expected)
		}
	}
	return nil
}

func verifyAudioGroup(ctx context.Context, ffmpeg, input, output string, sources []Stream, out Stream, v *AudioVerification, index int, merge bool) error {
	groupCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	decoders := make([]*audioDecoder, 0, len(sources)+1)
	defer func() {
		cancel()
		for _, d := range decoders {
			d.close()
		}
	}()
	for _, s := range sources {
		d, err := startAudioDecoder(groupCtx, ffmpeg, input, s.Index)
		if err != nil {
			return err
		}
		decoders = append(decoders, d)
	}
	d, err := startAudioDecoder(groupCtx, ffmpeg, output, out.Index)
	if err != nil {
		return err
	}
	decoders = append(decoders, d)
	reference := make([]float64, audioWindowFrames*2)
	actual := make([]float64, audioWindowFrames*2)
	for window := 0; ; window++ {
		if err := groupCtx.Err(); err != nil {
			return err
		}
		clear(reference)
		clear(actual)
		longest := 0
		var strongestChannelEnergy [2]float64
		for i, dec := range decoders[:len(sources)] {
			samples, err := dec.readBlock()
			if err != nil {
				return fmt.Errorf("source audio stream %d: %w", sources[i].Index, err)
			}
			n := len(samples)
			longest = max(longest, n)
			sourceIndex := index
			if merge {
				sourceIndex = i
			}
			v.SourceSamples[sourceIndex] += int64(n / 2)
			if dec.done {
				if err := validateDecodedAudioEnd("source", sources[i], v.SourceSamples[sourceIndex]); err != nil {
					return err
				}
			}
			for channel := 0; channel < 2; channel++ {
				energy := 0.0
				for j := channel; j < n; j += 2 {
					energy += samples[j] * samples[j]
				}
				strongestChannelEnergy[channel] = math.Max(strongestChannelEnergy[channel], energy)
			}
			for j, s := range samples {
				reference[j] += s / float64(len(sources))
			}
		}
		sourceEmpty := longest == 0
		samples, err := d.readBlock()
		if err != nil {
			return fmt.Errorf("output audio stream %d: %w", out.Index, err)
		}
		v.OutputSamples[index] += int64(len(samples) / 2)
		if d.done {
			if err := validateDecodedAudioEnd("output", out, v.OutputSamples[index]); err != nil {
				return err
			}
		}
		copy(actual, samples)
		longest = max(longest, len(samples))
		if longest == 0 {
			break
		}
		v.Windows++
		ref := reference[:longest]
		got := actual[:longest]
		activeThreshold := audioActiveRMS / float64(len(sources))
		active := false
		for channel := 0; channel < 2; channel++ {
			refRMS := audioChannelRMS(ref, channel)
			outputRMS := audioChannelRMS(got, channel)
			strongestRMS := math.Sqrt(strongestChannelEnergy[channel] / float64(longest/2))
			if merge && strongestRMS > audioActiveRMS && refRMS < strongestRMS/float64(len(sources))*0.05 {
				return fmt.Errorf("audio merge cancellation at %.3fs channel %d", float64(window*audioWindowMilliseconds)/1000, channel)
			}
			if refRMS <= activeThreshold {
				// Короткий хвост AAC после EOF не считается добавленным звуком.
				if (!sourceEmpty || longest/2 > audioDurationToleranceFrames) && outputRMS > math.Max(audioActiveRMS*3, refRMS*4) {
					return fmt.Errorf("unexpected audio during source silence at %.3fs channel %d", float64(window*audioWindowMilliseconds)/1000, channel)
				}
				continue
			}
			active = true
			ratio := outputRMS / refRMS
			if ratio < 0.5 || ratio > 2 {
				return &AudioQualityError{AtSeconds: float64(window*audioWindowMilliseconds) / 1000, Channel: channel, Metric: "energy", Value: ratio}
			}
			corr := audioAlignedCorrelation(ref, got, channel)
			v.MinimumCorrelation = math.Min(v.MinimumCorrelation, corr)
			if corr < audioMinimumCorrelation {
				return &AudioQualityError{AtSeconds: float64(window*audioWindowMilliseconds) / 1000, Channel: channel, Metric: "correlation", Value: corr}
			}
		}
		if active {
			v.ActiveWindows++
		}
	}
	return nil
}

func audioChannelRMS(samples []float64, channel int) float64 {
	sum := 0.0
	n := 0
	for i := channel; i < len(samples); i += 2 {
		sum += samples[i] * samples[i]
		n++
	}
	if n == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(n))
}

// Поиск лага использует не более 1024 точек на гипотезу. Решение всегда
// подтверждается на всех PCM-сэмплах, чтобы разреженная выборка не скрыла потерю.
func audioAlignedCorrelation(a, b []float64, channel int) float64 {
	corr := audioCorrelation(a, b, channel, 0)
	if corr >= audioMinimumCorrelation {
		return corr
	}
	stride := max(1, ((len(a)/2)+1023)/1024)
	bestLag, bestScore := 0, -2.0
	for lag := -audioRate / 50; lag <= audioRate/50; lag++ {
		score := audioCorrelationStep(a, b, channel, lag, stride)
		if score > bestScore {
			bestLag, bestScore = lag, score
		}
	}
	return math.Max(corr, audioCorrelation(a, b, channel, bestLag))
}
func audioCorrelation(a, b []float64, channel, lag int) float64 {
	return audioCorrelationStep(a, b, channel, lag, 1)
}
func audioCorrelationStep(a, b []float64, channel, lag, stride int) float64 {
	dot, aa, bb := 0.0, 0.0, 0.0
	for i := channel; i < len(a); i += 2 * stride {
		j := i + lag*2
		if j < 0 || j >= len(b) {
			continue
		}
		x, y := a[i], b[j]
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return math.Max(-1, math.Min(1, dot/math.Sqrt(aa*bb)))
}
func finiteAudio(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
func absAudioFrames(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
func audioWindowCount(n int64) int { return int((n + audioWindowFrames - 1) / audioWindowFrames) }

// Keep only the final diagnostic bytes, even when a decoder emits unbounded stderr.
type audioStderr struct{ data []byte }

func (b *audioStderr) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= 8192 {
		b.data = append(b.data[:0], p[len(p)-8192:]...)
	} else {
		if extra := len(b.data) + len(p) - 8192; extra > 0 {
			b.data = b.data[extra:]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

type audioDecoder struct {
	cmd     *exec.Cmd
	pipe    io.ReadCloser
	stderr  *audioStderr
	bytes   []byte
	samples []float64
	done    bool
	waited  bool
}

func startAudioDecoder(ctx context.Context, ffmpeg, path string, index int) (*audioDecoder, error) {
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-xerror", "-err_detect", "explode", "-threads", "1", "-i", path, "-map", fmt.Sprintf("0:%d", index), "-vn", "-sn", "-dn", "-af", "aresample=48000:async=1:first_pts=0", "-ac", "2", "-ar", "48000", "-threads", "1", "-f", "f32le", "pipe:1")
	stderr := &audioStderr{}
	cmd.Stderr = stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		pipe.Close()
		return nil, fmt.Errorf("start audio decoder: %w", err)
	}
	return &audioDecoder{cmd: cmd, pipe: pipe, stderr: stderr, bytes: make([]byte, audioWindowFrames*8), samples: make([]float64, audioWindowFrames*2)}, nil
}
func (d *audioDecoder) readBlock() ([]float64, error) {
	if d.done {
		return nil, nil
	}
	n, err := io.ReadFull(d.pipe, d.bytes)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	if err != nil {
		d.done = true
		waitErr := d.cmd.Wait()
		d.waited = true
		if waitErr != nil {
			return nil, fmt.Errorf("decode: %w: %s", waitErr, strings.TrimSpace(string(d.stderr.data)))
		}
	}
	if n%8 != 0 {
		return nil, fmt.Errorf("partial stereo PCM frame")
	}
	out := d.samples[:n/4]
	for i := range out {
		x := float64(math.Float32frombits(binary.LittleEndian.Uint32(d.bytes[i*4:])))
		if !finiteAudio(x) {
			return nil, fmt.Errorf("non-finite decoded PCM")
		}
		out[i] = x
	}
	return out, nil
}
func (d *audioDecoder) close() {
	d.pipe.Close()
	if !d.waited {
		if d.cmd.Process != nil {
			_ = d.cmd.Process.Kill()
		}
		_ = d.cmd.Wait()
		d.waited = true
	}
}
func audioSHA256(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 128*1024)
	for {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
