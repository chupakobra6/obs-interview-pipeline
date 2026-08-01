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
			{CodecType: "audio", CodecName: "aac"},
			{CodecType: "audio", CodecName: "aac"},
			{CodecType: "audio", CodecName: "aac"},
		},
		Format: Format{Duration: "59.98", Size: "600000"},
	}
	if err := ValidateCompressed(source, output, 1512, 982, 30); err != nil {
		t.Fatalf("ValidateCompressed() error = %v", err)
	}

	output.Streams = output.Streams[:3]
	if err := ValidateCompressed(source, output, 1512, 982, 30); err == nil {
		t.Fatal("ValidateCompressed() accepted a missing audio stream")
	}
}
