package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func audioTools(t *testing.T) (string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	return ffmpeg, ffprobe
}
func audioCommand(t *testing.T, ffmpeg string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args = append([]string{"-nostdin", "-v", "error", "-y"}, args...)
	if out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture: %v: %s", err, out)
	}
}
func audioSource(t *testing.T, ffmpeg string, filters ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.mkv")
	args := []string{}
	for _, filter := range filters {
		args = append(args, "-f", "lavfi", "-i", filter)
	}
	for i := range filters {
		args = append(args, "-map", string(rune('0'+i))+":a:0")
	}
	args = append(args, "-c:a", "pcm_f32le", path)
	audioCommand(t, ffmpeg, args...)
	return path
}
func audioProbe(t *testing.T, ffprobe, path string) Probe {
	t.Helper()
	p, err := Inspect(context.Background(), ffprobe, path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func verifyFixture(t *testing.T, ffmpeg, ffprobe, source, output string, merge bool) (AudioVerification, error) {
	t.Helper()
	return VerifyAudio(context.Background(), ffmpeg, source, output, audioProbe(t, ffprobe, source), audioProbe(t, ffprobe, output), merge)
}

func TestVerifyAudioValidAAC(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	for _, merge := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep-short-mic", true: "merge-short-mic"}[merge], func(t *testing.T) {
			source := audioSource(t, ffmpeg, "sine=frequency=443:sample_rate=48000:duration=6", "sine=frequency=997:sample_rate=48000:duration=1")
			output := filepath.Join(t.TempDir(), "output.mka")
			args := []string{"-i", source}
			if merge {
				args = append(args, "-filter_complex", "[0:a:0][0:a:1]amix=inputs=2:normalize=0:duration=longest,volume=0.5[a]", "-map", "[a]")
			} else {
				args = append(args, "-map", "0:a")
			}
			args = append(args, "-c:a", "aac", "-b:a", "96k", output)
			audioCommand(t, ffmpeg, args...)
			proof, err := verifyFixture(t, ffmpeg, ffprobe, source, output, merge)
			if err != nil {
				t.Fatal(err)
			}
			if !proof.Complete || proof.ActiveWindows == 0 || proof.MinimumDurationSeconds < 0.9 {
				t.Fatalf("incomplete proof: %+v", proof)
			}
			if err = proof.ValidateOutput(context.Background(), output); err != nil {
				t.Fatal(err)
			}
			if err = proof.ValidateSource(context.Background(), source); err != nil {
				t.Fatal(err)
			}
			sourceBytes, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			sourceInfo, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			sourceBytes[len(sourceBytes)-1] ^= 1
			if err = os.WriteFile(source, sourceBytes, 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.Chtimes(source, sourceInfo.ModTime(), sourceInfo.ModTime()); err != nil {
				t.Fatal(err)
			}
			if err = proof.ValidateSource(context.Background(), source); err == nil {
				t.Fatal("accepted changed source with unchanged size/mtime")
			}

			f, err := os.OpenFile(output, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.Write([]byte("changed"))
			f.Close()
			if err = proof.ValidateOutput(context.Background(), output); err == nil {
				t.Fatal("accepted changed output")
			}
		})
	}
}

func TestVerifyAudioRejectsLostTailAndWrongSignal(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := audioSource(t, ffmpeg, "sine=frequency=443:sample_rate=48000:duration=6")
	cases := map[string][]string{
		"silent-tail":                        {"-i", source, "-af", "volume=enable='gte(t,4)':volume=0", "-c:a", "aac", "-b:a", "96k"},
		"two-second-hole-straddling-windows": {"-i", source, "-af", "volume=enable='between(t,1,3)':volume=0", "-c:a", "aac", "-b:a", "96k"},
		"wrong-signal":                       {"-f", "lavfi", "-i", "sine=frequency=997:sample_rate=48000:duration=6", "-c:a", "aac", "-b:a", "96k"},
		"short-output":                       {"-i", source, "-t", "2", "-c:a", "aac", "-b:a", "96k"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "output.mka")
			audioCommand(t, ffmpeg, append(args, output)...)
			if _, err := verifyFixture(t, ffmpeg, ffprobe, source, output, false); err == nil {
				t.Fatal("accepted lost/replaced audio")
			}
		})
	}
}

func TestVerifyAudioSilenceAndStereo(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	for _, filter := range []string{"anullsrc=r=48000:cl=stereo:d=3", "aevalsrc=0.1*sin(2*PI*443*t)|-0.1*sin(2*PI*443*t):s=48000:d=3"} {
		source := audioSource(t, ffmpeg, filter)
		output := filepath.Join(t.TempDir(), "output.mka")
		audioCommand(t, ffmpeg, "-i", source, "-c:a", "aac", "-b:a", "96k", output)
		if _, err := verifyFixture(t, ffmpeg, ffprobe, source, output, false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVerifyAudioRejectsMergeCancellation(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := audioSource(t, ffmpeg, "aevalsrc=0.1*sin(2*PI*443*t):s=48000:d=4", "aevalsrc=-0.1*sin(2*PI*443*t):s=48000:d=4")
	output := filepath.Join(t.TempDir(), "output.mka")
	audioCommand(t, ffmpeg, "-i", source, "-filter_complex", "[0:a:0][0:a:1]amix=inputs=2:normalize=0,volume=0.5[a]", "-map", "[a]", "-c:a", "aac", output)
	if _, err := verifyFixture(t, ffmpeg, ffprobe, source, output, true); err == nil || !strings.Contains(err.Error(), "cancellation") {
		t.Fatalf("want cancellation error, got %v", err)
	}
}

func TestVerifyAudioRejectsIncompleteDecodeAndCancellation(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := audioSource(t, ffmpeg, "sine=frequency=443:sample_rate=48000:duration=6")
	output := filepath.Join(t.TempDir(), "output.mka")
	audioCommand(t, ffmpeg, "-i", source, "-c:a", "aac", output)
	srcProbe, outProbe := audioProbe(t, ffprobe, source), audioProbe(t, ffprobe, output)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := VerifyAudio(ctx, ffmpeg, source, output, srcProbe, outProbe, false); err == nil {
		t.Fatal("accepted canceled decode")
	}
	bytes, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, bytes[:len(bytes)/2], 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyAudio(context.Background(), ffmpeg, source, output, srcProbe, outProbe, false); err == nil {
		t.Fatal("accepted truncated decode")
	}
}

func TestVerifyAudioRejectsUnsupportedChannelsAndProof(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := audioSource(t, ffmpeg, "anullsrc=r=48000:cl=5.1:d=1")
	p := audioProbe(t, ffprobe, source)
	if _, err := VerifyAudio(context.Background(), ffmpeg, source, source, p, p, false); err == nil {
		t.Fatal("accepted unsupported multichannel")
	}
	if err := (AudioVerification{}).ValidateOutput(context.Background(), source); err == nil {
		t.Fatal("accepted empty proof")
	}
}

func TestVerifyAudioRejectsSignalAddedToSilence(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := audioSource(t, ffmpeg, "anullsrc=r=48000:cl=stereo:d=3")
	output := filepath.Join(t.TempDir(), "output.mka")
	audioCommand(t, ffmpeg, "-f", "lavfi", "-i", "sine=frequency=443:sample_rate=48000:duration=3", "-c:a", "aac", output)
	if _, err := verifyFixture(t, ffmpeg, ffprobe, source, output, false); err == nil {
		t.Fatal("accepted audio added to silence")
	}
}

func TestVerifyAudioRejectsNonFiniteAndEmptyDecode(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := audioSource(t, ffmpeg, "aevalsrc=nan:s=48000:d=1")
	p := audioProbe(t, ffprobe, source)
	if _, err := VerifyAudio(context.Background(), ffmpeg, source, source, p, p, false); err == nil {
		t.Fatal("accepted NaN decoded PCM")
	}
	// The media is real, but this source track's declared stream index does not
	// exist. A decoder with no usable audio must never certify an empty comparison.
	valid := audioSource(t, ffmpeg, "anullsrc=r=48000:cl=stereo:d=1")
	p = audioProbe(t, ffprobe, valid)
	p.Streams[0].Index = 99
	if _, err := VerifyAudio(context.Background(), ffmpeg, valid, valid, p, p, false); err == nil {
		t.Fatal("accepted empty decode")
	}
}

func TestAudioVerificationRejectsInvalidCoverage(t *testing.T) {
	valid := AudioVerification{Version: AudioVerificationVersion, Algorithm: AudioVerificationAlgorithm, Complete: true, SourceTracks: 1, OutputTracks: 1, SampleRate: 48000, WindowMilliseconds: 200, Windows: 5, MinimumDurationSeconds: 1, MinimumCorrelation: 1, SourceSamples: []int64{48000}, OutputSamples: []int64{48000}, SourceSHA256: strings.Repeat("b", 64), OutputSHA256: strings.Repeat("a", 64)}
	if err := valid.validateProof(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*AudioVerification){
		func(v *AudioVerification) { v.Complete = false },
		func(v *AudioVerification) { v.Version++ },
		func(v *AudioVerification) { v.Windows = 6 },
		func(v *AudioVerification) { v.ActiveWindows = 6 },
		func(v *AudioVerification) { v.MinimumCorrelation = 0.5 },
		func(v *AudioVerification) { v.OutputSamples = []int64{4000} },
		func(v *AudioVerification) { v.SourceSamples = []int64{0} },
		func(v *AudioVerification) { v.OutputSHA256 = "not-a-hash" },
		func(v *AudioVerification) { v.SourceSHA256 = "" },
		func(v *AudioVerification) { v.SourceSHA256 = "not-a-hash" },
	} {
		v := valid
		change(&v)
		if err := v.validateProof(); err == nil {
			t.Fatalf("accepted invalid proof: %+v", v)
		}
	}
}

func TestVerifyAudioMergeRetainsQuietSourceAndEachChannel(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	t.Run("quiet-source-attenuated-by-track-count", func(t *testing.T) {
		filters := []string{"aevalsrc=0.0008*sin(2*PI*443*t):s=48000:d=2"}
		for i := 1; i < 4; i++ {
			filters = append(filters, "anullsrc=r=48000:cl=mono:d=2")
		}
		source := audioSource(t, ffmpeg, filters...)
		output := filepath.Join(t.TempDir(), "output.mka")
		audioCommand(t, ffmpeg, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono:d=2", "-c:a", "aac", output)
		if _, err := verifyFixture(t, ffmpeg, ffprobe, source, output, true); err == nil {
			t.Fatal("accepted a lost active source hidden by merge attenuation")
		}
	})
	t.Run("cancellation-in-one-channel", func(t *testing.T) {
		source := audioSource(t, ffmpeg, "aevalsrc=0.1*sin(2*PI*443*t)|0.1*sin(2*PI*997*t):s=48000:d=2", "aevalsrc=-0.1*sin(2*PI*443*t)|0.1*sin(2*PI*997*t):s=48000:d=2")
		output := filepath.Join(t.TempDir(), "output.mka")
		audioCommand(t, ffmpeg, "-i", source, "-filter_complex", "[0:a:0][0:a:1]amix=inputs=2:normalize=0,volume=0.5[a]", "-map", "[a]", "-c:a", "aac", output)
		if _, err := verifyFixture(t, ffmpeg, ffprobe, source, output, true); err == nil || !strings.Contains(err.Error(), "cancellation") {
			t.Fatalf("want one-channel cancellation refusal, got %v", err)
		}
	})
}

func TestVerifyAudioRejectsSourceChangedDuringDecode(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := audioSource(t, ffmpeg, "sine=frequency=443:sample_rate=48000:duration=3")
	output := filepath.Join(t.TempDir(), "output.mka")
	audioCommand(t, ffmpeg, "-i", source, "-c:a", "aac", output)
	sourceProbe, outputProbe := audioProbe(t, ffprobe, source), audioProbe(t, ffprobe, output)
	wrapper := filepath.Join(t.TempDir(), "ffmpeg-wrapper")
	t.Setenv("AUDIO_TEST_FFMPEG", ffmpeg)
	t.Setenv("AUDIO_TEST_SOURCE", source)
	script := `#!/bin/sh
"$AUDIO_TEST_FFMPEG" "$@"
result=$?
for arg do
 if [ "$arg" = "$AUDIO_TEST_SOURCE" ]; then
  printf changed >> "$AUDIO_TEST_SOURCE"
  break
 fi
done
exit "$result"
`
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAudio(context.Background(), wrapper, source, output, sourceProbe, outputProbe, false); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("want source changed refusal, got %v", err)
	}
}

func TestReviewHighFrequencyLoss(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := audioSource(t, ffmpeg, "sine=frequency=6000:sample_rate=48000:duration=4,afade=t=in:d=0.5,afade=t=out:st=3.5:d=0.5")
	output := filepath.Join(t.TempDir(), "output.mka")
	audioCommand(t, ffmpeg, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono:d=4", "-c:a", "aac", "-b:a", "96k", output)
	proof, err := verifyFixture(t, ffmpeg, ffprobe, source, output, false)
	t.Logf("high-frequency lost proof=%+v error=%v", proof, err)
	if err == nil {
		t.Fatal("accepted completely lost 6 kHz audible source")
	}
}

func TestReviewSubsecondDropout(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := audioSource(t, ffmpeg, "sine=frequency=443:sample_rate=48000:duration=4")
	output := filepath.Join(t.TempDir(), "output.mka")
	audioCommand(t, ffmpeg, "-i", source, "-af", "volume=enable='between(t,0.75,1.20)':volume=0", "-c:a", "aac", "-b:a", "96k", output)
	proof, err := verifyFixture(t, ffmpeg, ffprobe, source, output, false)
	t.Logf("450 ms lost proof=%+v error=%v", proof, err)
	if err == nil {
		t.Fatal("accepted 450 ms missing signal in a 2s window")
	}
}

func TestReviewOffsetGapMix(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := filepath.Join(t.TempDir(), "source.mkv")
	audioCommand(t, ffmpeg,
		"-f", "lavfi", "-i", "sine=frequency=443:sample_rate=48000:duration=8",
		"-f", "lavfi", "-i", "sine=frequency=997:sample_rate=48000:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000:duration=1",
		"-filter_complex", "[1:a]aselect=not(between(t\\,2\\,3)),asetpts=PTS+0.75/TB[late]",
		"-map", "0:a", "-map", "[late]", "-map", "2:a", "-c:a", "pcm_f32le", source)
	output := filepath.Join(t.TempDir(), "output.mka")
	filter := "[0:a:0]aresample=48000:async=1:first_pts=0,aformat=channel_layouts=stereo[a0];[0:a:1]aresample=48000:async=1:first_pts=0,aformat=channel_layouts=stereo[a1];[0:a:2]aresample=48000:async=1:first_pts=0,aformat=channel_layouts=stereo[a2];[a0][a1][a2]amix=inputs=3:duration=longest:normalize=0,volume=1/3[aout]"
	audioCommand(t, ffmpeg, "-i", source, "-filter_complex", filter, "-map", "[aout]", "-c:a", "aac", "-b:a", "96k", output)
	proof, err := verifyFixture(t, ffmpeg, ffprobe, source, output, true)
	t.Logf("offset/gap proof=%+v error=%v", proof, err)
	if err != nil {
		t.Fatal(err)
	}
}

func TestReviewOutputChangedDuringVerification(t *testing.T) {
	ffmpeg, ffprobe := audioTools(t)
	source := audioSource(t, ffmpeg, "sine=frequency=443:sample_rate=48000:duration=4")
	dir := t.TempDir()
	output := filepath.Join(dir, "output.mka")
	replacement := filepath.Join(dir, "silence.mka")
	audioCommand(t, ffmpeg, "-i", source, "-c:a", "aac", "-b:a", "96k", output)
	audioCommand(t, ffmpeg, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono:d=4", "-c:a", "aac", "-b:a", "96k", replacement)
	originalInfo, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	replacementBytes, err := os.ReadFile(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(replacementBytes)) >= originalInfo.Size() {
		t.Fatal("replacement must fit original file size")
	}
	padded := make([]byte, originalInfo.Size())
	copy(padded, replacementBytes)
	if err = os.WriteFile(replacement, padded, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(replacement, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUDIO_TEST_FFMPEG", ffmpeg)
	t.Setenv("AUDIO_TEST_OUTPUT", output)
	t.Setenv("AUDIO_TEST_REPLACEMENT", replacement)
	script := `#!/bin/sh
"$AUDIO_TEST_FFMPEG" "$@"
result=$?
for arg do
 if [ "$arg" = "$AUDIO_TEST_OUTPUT" ]; then
  cp -p "$AUDIO_TEST_REPLACEMENT" "$AUDIO_TEST_OUTPUT" || exit 1
  break
 fi
done
exit "$result"
`
	wrapper := filepath.Join(dir, "ffmpeg-wrapper")
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	proof, err := verifyFixture(t, wrapper, ffprobe, source, output, false)
	t.Logf("mutated-output proof=%+v error=%v", proof, err)
	if err == nil || !strings.Contains(err.Error(), "output changed") {
		t.Fatalf("want output changed refusal, got %v", err)
	}
	afterInfo, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(originalInfo, afterInfo) || originalInfo.Size() != afterInfo.Size() || !originalInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("fixture did not retain output identity/size/mtime")
	}
}

func TestAudioCorrelationConfirmsFullSamplesAfterBoundedLagSearch(t *testing.T) {
	a := make([]float64, audioWindowFrames*2)
	state := uint32(17)
	for i := 0; i < audioWindowFrames; i++ {
		state = state*1664525 + 1013904223
		value := float64(int32(state)) / 2147483648
		a[2*i] = value
		a[2*i+1] = value
	}
	t.Run("valid-ten-ms-offset", func(t *testing.T) {
		b := make([]float64, len(a))
		copy(b[480*2:], a)
		if corr := audioAlignedCorrelation(a, b, 0); corr < audioMinimumCorrelation {
			t.Fatalf("rejected valid lag: %.4f", corr)
		}
	})
	t.Run("coarse-alias-must-not-pass", func(t *testing.T) {
		b := make([]float64, len(a))
		for i, v := range a {
			b[i] = -v
			if (i/2)%10 == 0 {
				b[i] = v
			}
		}
		if corr := audioAlignedCorrelation(a, b, 0); corr >= audioMinimumCorrelation {
			t.Fatalf("accepted sparse-only agreement: %.4f", corr)
		}
	})
}
