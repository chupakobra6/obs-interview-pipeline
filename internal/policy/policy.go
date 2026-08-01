package policy

import (
	"fmt"
)

const (
	AudioPreserve = "preserve"
	AudioMerge    = "merge"
)

type Options struct {
	DeleteSource bool   `json:"delete_source"`
	AudioMode    string `json:"audio_mode"`
}

func (o Options) Validate() error {
	switch o.AudioMode {
	case AudioPreserve, AudioMerge:
		return nil
	default:
		return fmt.Errorf("audio mode must be %q or %q", AudioPreserve, AudioMerge)
	}
}
