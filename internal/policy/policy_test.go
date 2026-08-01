package policy

import "testing"

func TestOptionsValidate(t *testing.T) {
	for _, mode := range []string{AudioPreserve, AudioMerge} {
		if err := (Options{AudioMode: mode}).Validate(); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
	}
	if err := (Options{AudioMode: "master"}).Validate(); err == nil {
		t.Fatal("unsupported audio mode was accepted")
	}
}
