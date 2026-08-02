package asr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
)

func TestDecodeHarvestResponseValidatesContractAndTranscript(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.txt")
	if err := os.WriteFile(transcriptPath, []byte("Проверка общей расшифровки."), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(harvestResponse{
		ContractVersion:   harvestContractVersion,
		Status:            "ok",
		ProfileID:         harvestProfileID,
		ValidationStatus:  harvestValidationCoverage,
		Text:              "Проверка общей расшифровки.",
		SpeechDetected:    true,
		Engine:            "whispercpp",
		Backend:           json.RawMessage(`{"owned_by":"telegram-harvest"}`),
		Diagnostics:       json.RawMessage(`{"opaque":true}`),
		LanguageDetection: 750000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeHarvestResponse(payload, transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContractVersion != harvestContractVersion || result.ProfileID != harvestProfileID ||
		result.ValidationStatus != harvestValidationCoverage || result.Engine != "whispercpp" ||
		result.LanguageDetection != 750000000 || !json.Valid(result.Diagnostics) {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestToolEnvironmentProvidesHomebrewToLaunchAgentChild(t *testing.T) {
	runner := Runner{Config: config.Config{FFmpegCommand: "/opt/homebrew/bin/ffmpeg"}}
	var pathValue string
	for _, value := range runner.toolEnvironment() {
		if strings.HasPrefix(value, "PATH=") {
			pathValue = value
		}
	}
	if !strings.Contains(pathValue, "/opt/homebrew/bin") || !strings.Contains(pathValue, "/usr/bin") {
		t.Fatalf("unexpected tool PATH: %s", pathValue)
	}
}

func TestTranscribeArgsUseUnifiedAdaptiveProfile(t *testing.T) {
	args := transcribeArgs("/tmp/interview.mp4", "/tmp/transcript.txt")
	joined := strings.Join(args, " ")
	for _, want := range []string{"--profile main", "transcribe-file", "--input /tmp/interview.mp4", "--output /tmp/transcript.txt"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("transcribe args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "--trusted-long-form") {
		t.Fatalf("transcribe args retain removed caller-owned ASR mode: %q", joined)
	}
}

func TestDecodeHarvestResponseRejectsWrongPublicContract(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.txt")
	if err := os.WriteFile(transcriptPath, []byte("file text"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := harvestResponse{
		ContractVersion:  harvestContractVersion,
		Status:           "ok",
		ProfileID:        harvestProfileID,
		ValidationStatus: harvestValidationCoverage,
		Text:             "file text",
	}
	tests := []struct {
		name   string
		mutate func(*harvestResponse)
		want   string
	}{
		{name: "version", mutate: func(value *harvestResponse) { value.ContractVersion-- }, want: "contract"},
		{name: "status", mutate: func(value *harvestResponse) { value.Status = "error" }, want: "status"},
		{name: "profile", mutate: func(value *harvestResponse) { value.ProfileID = "legacy-profile" }, want: "profile"},
		{name: "validation", mutate: func(value *harvestResponse) { value.ValidationStatus = "no-speech" }, want: "validation status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			payload, _ := json.Marshal(value)
			if _, err := decodeHarvestResponse(payload, transcriptPath); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDecodeHarvestResponseAcceptsAdaptiveShortResult(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.txt")
	if err := os.WriteFile(transcriptPath, []byte("Короткая запись."), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(harvestResponse{
		ContractVersion: harvestContractVersion, Status: "ok", ProfileID: harvestProfileID,
		ValidationStatus: harvestValidationTranscribed, Text: "Короткая запись.", SpeechDetected: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeHarvestResponse(payload, transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.ValidationStatus != harvestValidationTranscribed {
		t.Fatalf("validation status = %q", result.ValidationStatus)
	}
}

func TestDecodeHarvestResponseRejectsTranscriptMismatch(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.txt")
	if err := os.WriteFile(transcriptPath, []byte("file text"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := harvestResponse{
		ContractVersion:  harvestContractVersion,
		Status:           "ok",
		ProfileID:        harvestProfileID,
		ValidationStatus: harvestValidationCoverage,
	}
	base.Text = "JSON text"
	payload, _ := json.Marshal(base)
	if _, err := decodeHarvestResponse(payload, transcriptPath); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("unexpected mismatch error: %v", err)
	}
}

func TestDecodeHarvestResponseRejectsEmptyInterviewTranscript(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.txt")
	if err := os.WriteFile(transcriptPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(harvestResponse{
		ContractVersion:  harvestContractVersion,
		Status:           "ok",
		ProfileID:        harvestProfileID,
		ValidationStatus: harvestValidationCoverage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeHarvestResponse(payload, transcriptPath); err == nil || !strings.Contains(err.Error(), "no transcript") {
		t.Fatalf("unexpected empty transcript error: %v", err)
	}
}

func TestValidateRuntimeCheckResponseUsesPublicContract(t *testing.T) {
	payload := []byte(`{"contract_version":4,"status":"ok","profile_id":"adaptive-media-v1","validation_status":"runtime-ready","backend":{"arbitrary":true}}`)
	if err := ValidateRuntimeCheckResponse(payload); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		[]byte(`{"contract_version":3,"status":"ok","profile_id":"adaptive-media-v1","validation_status":"runtime-ready"}`),
		[]byte(`{"contract_version":4,"status":"ok","profile_id":"legacy-profile","validation_status":"runtime-ready"}`),
		[]byte(`{"contract_version":4,"status":"ok","profile_id":"adaptive-media-v1","validation_status":"coverage-validated"}`),
	} {
		if err := ValidateRuntimeCheckResponse(invalid); err == nil {
			t.Fatalf("invalid check response was accepted: %s", invalid)
		}
	}
}
