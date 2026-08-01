package media

import "testing"

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
