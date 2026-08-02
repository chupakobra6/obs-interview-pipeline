package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
)

const (
	harvestContractVersion        = 4
	harvestProfileID              = "adaptive-media-v1"
	harvestValidationRuntimeReady = "runtime-ready"
	harvestValidationTranscribed  = "transcribed"
	harvestValidationCoverage     = "coverage-validated"
)

type Result struct {
	ContractVersion   int             `json:"contract_version"`
	ProfileID         string          `json:"profile_id"`
	ValidationStatus  string          `json:"validation_status"`
	Text              string          `json:"text"`
	SpeechDetected    bool            `json:"speech_detected"`
	Engine            string          `json:"engine"`
	Backend           json.RawMessage `json:"backend"`
	FFmpeg            time.Duration   `json:"ffmpeg"`
	ModelColdStart    time.Duration   `json:"model_cold_start"`
	SpeechGate        time.Duration   `json:"speech_gate"`
	LongFormPrep      time.Duration   `json:"long_form_preparation"`
	LanguageDetection time.Duration   `json:"language_detection"`
	LeadingOffset     float64         `json:"leading_speech_offset_seconds,omitempty"`
	Diagnostics       json.RawMessage `json:"diagnostics,omitempty"`
	Inference         time.Duration   `json:"inference"`
	Total             time.Duration   `json:"total"`
	MetalConfirmed    bool            `json:"metal_confirmed"`
}

type Transcriber interface {
	Transcribe(context.Context, string, string) (Result, error)
}

type Runner struct {
	Config config.Config
}

type harvestResponse struct {
	ContractVersion   int             `json:"contract_version"`
	Status            string          `json:"status"`
	ProfileID         string          `json:"profile_id"`
	ValidationStatus  string          `json:"validation_status"`
	Text              string          `json:"text"`
	SpeechDetected    bool            `json:"speech_detected"`
	MetalConfirmed    bool            `json:"metal_confirmed"`
	Engine            string          `json:"engine"`
	Backend           json.RawMessage `json:"backend"`
	FFmpeg            time.Duration   `json:"ffmpeg"`
	ModelColdStart    time.Duration   `json:"model_cold_start"`
	SpeechGate        time.Duration   `json:"speech_gate"`
	LongFormPrep      time.Duration   `json:"long_form_preparation"`
	LanguageDetection time.Duration   `json:"language_detection"`
	LeadingOffset     float64         `json:"leading_speech_offset_seconds,omitempty"`
	Diagnostics       json.RawMessage `json:"diagnostics"`
	Inference         time.Duration   `json:"inference"`
	Total             time.Duration   `json:"total"`
}

func (r Runner) Transcribe(ctx context.Context, inputPath, workDir string) (Result, error) {
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create ASR work dir: %w", err)
	}
	if err := r.buildHarvest(ctx); err != nil {
		return Result{}, err
	}
	transcriptPath := filepath.Join(workDir, "telegram-harvest-transcript.txt")
	command := exec.CommandContext(
		ctx,
		r.Config.TelegramHarvestCommand,
		transcribeArgs(inputPath, transcriptPath)...,
	)
	command.Dir = r.Config.TelegramHarvestRoot
	command.Env = r.toolEnvironment()
	command.Cancel = func() error {
		return command.Process.Signal(syscall.SIGTERM)
	}
	command.WaitDelay = 10 * time.Second
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return Result{}, fmt.Errorf("telegram-harvest ASR: %w: %s", err, compact(stderr.Bytes()))
	}
	return decodeHarvestResponse(stdout.Bytes(), transcriptPath)
}

func transcribeArgs(inputPath, transcriptPath string) []string {
	return []string{
		"--profile", "main",
		"transcribe-file",
		"--input", inputPath,
		"--output", transcriptPath,
	}
}

func (r Runner) buildHarvest(ctx context.Context) error {
	command := exec.CommandContext(ctx, r.Config.MakeCommand, "-s", "-C", r.Config.TelegramHarvestRoot, "build")
	command.Env = r.toolEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build telegram-harvest ASR entrypoint: %w: %s", err, compact(output))
	}
	return nil
}

func (r Runner) toolEnvironment() []string {
	pathValue := strings.Join([]string{
		filepath.Dir(r.Config.FFmpegCommand),
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}, string(os.PathListSeparator))
	environment := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "PATH=") {
			environment = append(environment, value)
		}
	}
	return append(environment, "PATH="+pathValue)
}

func decodeHarvestResponse(payload []byte, transcriptPath string) (Result, error) {
	var response harvestResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return Result{}, fmt.Errorf("decode telegram-harvest ASR response: %w: %s", err, compact(payload))
	}
	if err := validateHarvestContract(
		response.ContractVersion,
		response.Status,
		response.ProfileID,
		response.ValidationStatus,
		[]string{harvestValidationTranscribed, harvestValidationCoverage},
		"",
	); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(response.Text) == "" {
		return Result{}, fmt.Errorf("telegram-harvest ASR returned no transcript for interview input")
	}
	transcript, err := os.ReadFile(transcriptPath)
	if err != nil {
		return Result{}, fmt.Errorf("read telegram-harvest transcript: %w", err)
	}
	if strings.TrimSpace(string(transcript)) != strings.TrimSpace(response.Text) {
		return Result{}, fmt.Errorf("telegram-harvest transcript file differs from its JSON response")
	}
	return Result{
		ContractVersion:   response.ContractVersion,
		ProfileID:         response.ProfileID,
		ValidationStatus:  response.ValidationStatus,
		Text:              response.Text,
		SpeechDetected:    response.SpeechDetected,
		Engine:            response.Engine,
		Backend:           response.Backend,
		FFmpeg:            response.FFmpeg,
		ModelColdStart:    response.ModelColdStart,
		SpeechGate:        response.SpeechGate,
		LongFormPrep:      response.LongFormPrep,
		LanguageDetection: response.LanguageDetection,
		LeadingOffset:     response.LeadingOffset,
		Diagnostics:       response.Diagnostics,
		Inference:         response.Inference,
		Total:             response.Total,
		MetalConfirmed:    response.MetalConfirmed,
	}, nil
}

// ValidateRuntimeCheckResponse validates only the public Harvest contract.
// Model, accelerator, decode and VAD policy are owned and verified by Harvest.
func ValidateRuntimeCheckResponse(payload []byte) error {
	var response struct {
		ContractVersion  int    `json:"contract_version"`
		Status           string `json:"status"`
		ProfileID        string `json:"profile_id"`
		ValidationStatus string `json:"validation_status"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return fmt.Errorf("decode telegram-harvest ASR check: %w: %s", err, compact(payload))
	}
	return validateHarvestContract(response.ContractVersion, response.Status, response.ProfileID, response.ValidationStatus, []string{harvestValidationRuntimeReady}, " check")
}

func validateHarvestContract(contractVersion int, status, profileID, validationStatus string, expectedValidationStatuses []string, operation string) error {
	prefix := "telegram-harvest ASR" + operation
	if contractVersion != harvestContractVersion {
		return fmt.Errorf("unsupported %s contract %d", prefix, contractVersion)
	}
	if status != "ok" {
		return fmt.Errorf("%s returned status %q", prefix, status)
	}
	if profileID != harvestProfileID {
		return fmt.Errorf("%s returned profile %q, want %q", prefix, profileID, harvestProfileID)
	}
	for _, expected := range expectedValidationStatuses {
		if validationStatus == expected {
			return nil
		}
	}
	return fmt.Errorf("%s returned validation status %q, want one of %q", prefix, validationStatus, expectedValidationStatuses)
}

func compact(value []byte) string {
	text := strings.TrimSpace(strings.ReplaceAll(string(value), "\n", " "))
	if len(text) > 1000 {
		return text[len(text)-1000:]
	}
	return text
}
