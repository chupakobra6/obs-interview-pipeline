package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
)

type Result struct {
	Text           string        `json:"text"`
	SpeechDetected bool          `json:"speech_detected"`
	ModelColdStart time.Duration `json:"model_cold_start"`
	SpeechGate     time.Duration `json:"speech_gate"`
	Inference      time.Duration `json:"inference"`
	Total          time.Duration `json:"total"`
	MetalConfirmed bool          `json:"metal_confirmed"`
}

type Transcriber interface {
	Transcribe(context.Context, string, string) (Result, error)
}

type Runner struct {
	Config config.Config
}

func (r Runner) Transcribe(ctx context.Context, inputPath, workDir string) (Result, error) {
	started := time.Now()
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create ASR work dir: %w", err)
	}
	wavFile, err := os.CreateTemp(workDir, ".audio-*.wav")
	if err != nil {
		return Result{}, fmt.Errorf("create ASR WAV: %w", err)
	}
	wavPath := wavFile.Name()
	if err := wavFile.Close(); err != nil {
		return Result{}, fmt.Errorf("close ASR WAV: %w", err)
	}
	defer os.Remove(wavPath)

	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", inputPath,
		"-map", "0:a:0",
		"-vn", "-ac", "1", "-ar", "16000", "-sample_fmt", "s16",
		wavPath,
	}
	if output, err := exec.CommandContext(ctx, r.Config.FFmpegCommand, args...).CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("extract ASR audio: %w: %s", err, compact(output))
	}

	gateStarted := time.Now()
	speechDetected, err := r.hasSpeech(ctx, wavPath)
	gateDuration := time.Since(gateStarted)
	if err != nil {
		return Result{}, err
	}
	if !speechDetected {
		return Result{
			SpeechDetected: false,
			SpeechGate:     gateDuration,
			Total:          time.Since(started),
		}, nil
	}

	coldStarted := time.Now()
	session, err := r.startServer(ctx)
	coldDuration := time.Since(coldStarted)
	if err != nil {
		return Result{}, err
	}
	defer session.Close()

	inferenceStarted := time.Now()
	text, err := session.infer(ctx, wavPath)
	inferenceDuration := time.Since(inferenceStarted)
	if err != nil {
		return Result{}, err
	}
	text = stripTerminalHallucinations(strings.TrimSpace(text))
	return Result{
		Text:           text,
		SpeechDetected: true,
		ModelColdStart: coldDuration,
		SpeechGate:     gateDuration,
		Inference:      inferenceDuration,
		Total:          time.Since(started),
		MetalConfirmed: true,
	}, nil
}

func (r Runner) hasSpeech(ctx context.Context, wavPath string) (bool, error) {
	args := []string{
		"--no-prints",
		"--vad-model", r.Config.WhisperGateModelPath,
		"--vad-threshold", "0.5",
		"--vad-min-silence-duration-ms", "100",
		"--vad-min-speech-duration-ms", "250",
		"--vad-speech-pad-ms", "30",
		"--file", wavPath,
	}
	output, err := exec.CommandContext(ctx, r.Config.WhisperGateCommand, args...).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("whisper speech gate: %w: %s", err, compact(output))
	}
	value := string(output)
	switch {
	case strings.Contains(value, "Detected 0 speech segments:"):
		return false, nil
	case strings.Contains(value, "speech segments:"):
		return true, nil
	default:
		return false, fmt.Errorf("whisper speech gate returned an unrecognized result: %s", compact(output))
	}
}

type serverSession struct {
	command  *exec.Cmd
	baseURL  string
	client   *http.Client
	waitDone chan error
	stdout   *cappedBuffer
	stderr   *cappedBuffer
	stopOnce sync.Once
}

type whisperResponse struct {
	Text  string `json:"text"`
	Error string `json:"error"`
}

func (r Runner) startServer(ctx context.Context) (*serverSession, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("reserve whisper port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return nil, fmt.Errorf("release whisper port: %w", err)
	}
	stdout := &cappedBuffer{}
	stderr := &cappedBuffer{}
	args := []string{
		"--model", r.Config.WhisperModelPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--threads", "4",
		"--processors", "1",
		"--language", "ru",
		"--no-timestamps",
		"--no-language-probabilities",
	}
	command := exec.Command(r.Config.WhisperServerCommand, args...)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start whisper server: %w", err)
	}
	session := &serverSession{
		command:  command,
		baseURL:  fmt.Sprintf("http://127.0.0.1:%d", port),
		client:   &http.Client{},
		waitDone: make(chan error, 1),
		stdout:   stdout,
		stderr:   stderr,
	}
	go func() { session.waitDone <- command.Wait() }()

	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, requestErr := http.NewRequestWithContext(readyCtx, http.MethodGet, session.baseURL+"/health", nil)
		if requestErr == nil {
			response, getErr := session.client.Do(request)
			if getErr == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					evidence := strings.ToLower(stdout.String() + "\n" + stderr.String())
					if !strings.Contains(evidence, "ggml_metal_init: found device") {
						session.Close()
						return nil, fmt.Errorf("whisper server did not confirm Metal: %s", session.detail())
					}
					if strings.Contains(evidence, "core ml model loaded") || strings.Contains(evidence, "coreml = 1") {
						session.Close()
						return nil, fmt.Errorf("whisper server unexpectedly activated Core ML")
					}
					return session, nil
				}
			}
		}
		select {
		case waitErr := <-session.waitDone:
			return nil, fmt.Errorf("whisper server exited before readiness: %v: %s", waitErr, session.detail())
		case <-readyCtx.Done():
			session.Close()
			return nil, fmt.Errorf("wait for whisper server: %w: %s", readyCtx.Err(), session.detail())
		case <-ticker.C:
		}
	}
}

func (s *serverSession) infer(ctx context.Context, wavPath string) (string, error) {
	file, err := os.Open(wavPath)
	if err != nil {
		return "", fmt.Errorf("open ASR WAV: %w", err)
	}
	bodyReader, bodyWriter := io.Pipe()
	writer := multipart.NewWriter(bodyWriter)
	contentType := writer.FormDataContentType()
	go func() {
		defer file.Close()
		part, partErr := writer.CreateFormFile("file", filepath.Base(wavPath))
		if partErr == nil {
			_, partErr = io.Copy(part, file)
		}
		fields := map[string]string{
			"response_format": "verbose_json",
			"language":        "ru",
			"temperature":     "0",
			"temperature_inc": "0.2",
			"best_of":         "2",
			"beam_size":       "5",
			"no_speech_thold": "0.6",
			"logprob_thold":   "-1",
			"entropy_thold":   "2.4",
			"suppress_nst":    "false",
		}
		if partErr == nil {
			for key, value := range fields {
				if partErr = writer.WriteField(key, value); partErr != nil {
					break
				}
			}
		}
		if partErr == nil {
			partErr = writer.Close()
		}
		_ = bodyWriter.CloseWithError(partErr)
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/inference", bodyReader)
	if err != nil {
		return "", fmt.Errorf("prepare whisper request: %w", err)
	}
	request.Header.Set("Content-Type", contentType)
	response, err := s.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("whisper inference: %w: %s", err, s.detail())
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return "", fmt.Errorf("read whisper response: %w", err)
	}
	var decoded whisperResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", fmt.Errorf("decode whisper response: %w: %s", err, compact(payload))
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whisper status %s: %s", response.Status, strings.TrimSpace(decoded.Error))
	}
	if strings.TrimSpace(decoded.Error) != "" {
		return "", fmt.Errorf("whisper inference: %s", strings.TrimSpace(decoded.Error))
	}
	return decoded.Text, nil
}

func (s *serverSession) Close() {
	s.stopOnce.Do(func() {
		if s.command == nil || s.command.Process == nil {
			return
		}
		_ = s.command.Process.Signal(syscall.SIGTERM)
		select {
		case <-s.waitDone:
		case <-time.After(5 * time.Second):
			_ = s.command.Process.Kill()
			select {
			case <-s.waitDone:
			case <-time.After(time.Second):
			}
		}
	})
}

func (s *serverSession) detail() string {
	return compact([]byte(s.stdout.String() + "\n" + s.stderr.String()))
}

type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	const maximum = 256 << 10
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(p)
	if len(p) >= maximum {
		b.buf.Reset()
		_, _ = b.buf.Write(p[len(p)-maximum:])
		return written, nil
	}
	if overflow := b.buf.Len() + len(p) - maximum; overflow > 0 {
		current := append([]byte(nil), b.buf.Bytes()[overflow:]...)
		b.buf.Reset()
		_, _ = b.buf.Write(current)
	}
	_, _ = b.buf.Write(p)
	return written, nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func compact(value []byte) string {
	text := strings.TrimSpace(strings.ReplaceAll(string(value), "\n", " "))
	if len(text) > 1000 {
		return text[len(text)-1000:]
	}
	return text
}

var terminalHallucinations = map[string]struct{}{
	"продолжение следует":          {},
	"субтитры сделал dimatorzok":   {},
	"субтитры создавал dimatorzok": {},
	"спасибо за просмотр":          {},
	"подпишись":                    {},
}

func stripTerminalHallucinations(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for len(lines) > 0 {
		last := strings.ToLower(strings.Trim(strings.TrimSpace(lines[len(lines)-1]), ".,!?:;—–-«»\"' "))
		if _, known := terminalHallucinations[last]; !known {
			break
		}
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
