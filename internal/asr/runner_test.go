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
		ContractVersion: harvestContractVersion,
		Status:          "ok",
		Text:            "Проверка общей расшифровки.",
		SpeechDetected:  true,
		MetalConfirmed:  true,
		Engine:          "whispercpp",
		Backend:         json.RawMessage(`{"backend":"whispercpp","accelerator":"metal"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeHarvestResponse(payload, transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContractVersion != harvestContractVersion || !result.MetalConfirmed || result.Engine != "whispercpp" {
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

func TestDecodeHarvestResponseRejectsUnconfirmedMetalAndMismatch(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.txt")
	if err := os.WriteFile(transcriptPath, []byte("file text"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := harvestResponse{
		ContractVersion: harvestContractVersion,
		Status:          "ok",
		Text:            "file text",
		SpeechDetected:  true,
		Engine:          "whispercpp",
		Backend:         json.RawMessage(`{"backend":"whispercpp"}`),
	}
	payload, _ := json.Marshal(base)
	if _, err := decodeHarvestResponse(payload, transcriptPath); err == nil || !strings.Contains(err.Error(), "confirm Metal") {
		t.Fatalf("unexpected Metal error: %v", err)
	}
	base.MetalConfirmed = true
	base.Text = "JSON text"
	payload, _ = json.Marshal(base)
	if _, err := decodeHarvestResponse(payload, transcriptPath); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("unexpected mismatch error: %v", err)
	}
}
