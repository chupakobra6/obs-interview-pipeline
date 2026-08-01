package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/processor"
	jobqueue "github.com/chupakobra6/obs-interview-pipeline/internal/queue"
)

const executableName = "obs-interview-processor"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "resolve home directory: %v\n", err)
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch args[0] {
	case "install":
		if err := install(home, stdout); err != nil {
			fmt.Fprintf(stderr, "install: %v\n", err)
			return 1
		}
		return 0
	case "doctor":
		cfg, _, err := loadConfigArgs(home, "doctor", args[1:])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if err := doctor(ctx, cfg, stdout); err != nil {
			fmt.Fprintf(stderr, "doctor: %v\n", err)
			return 1
		}
		return 0
	case "enqueue":
		cfg, rest, err := loadConfigArgs(home, "enqueue", args[1:])
		if err != nil || len(rest) != 1 {
			if err == nil {
				err = fmt.Errorf("enqueue requires one recording path")
			}
			fmt.Fprintln(stderr, err)
			return 2
		}
		job, err := jobqueue.Enqueue(cfg, rest[0])
		if err != nil {
			fmt.Fprintf(stderr, "enqueue: %v\n", err)
			return 1
		}
		printJSON(stdout, job)
		return 0
	case "process":
		cfg, rest, err := loadConfigArgs(home, "process", args[1:])
		if err != nil || len(rest) != 1 {
			if err == nil {
				err = fmt.Errorf("process requires one recording path")
			}
			fmt.Fprintln(stderr, err)
			return 2
		}
		result, err := processor.New(cfg).Process(ctx, rest[0])
		if err != nil {
			fmt.Fprintf(stderr, "process: %v\n", err)
			return 1
		}
		printJSON(stdout, result)
		return 0
	case "run-queue":
		cfg, rest, err := loadConfigArgs(home, "run-queue", args[1:])
		if err != nil || len(rest) != 0 {
			if err == nil {
				err = fmt.Errorf("run-queue takes no positional arguments")
			}
			fmt.Fprintln(stderr, err)
			return 2
		}
		if err := runQueue(ctx, cfg, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "run queue: %v\n", err)
			return 1
		}
		return 0
	default:
		usage(stderr)
		return 2
	}
}

func loadConfigArgs(home, name string, args []string) (config.Config, []string, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", config.DefaultPath(home), "config path")
	if err := flags.Parse(args); err != nil {
		return config.Config{}, nil, err
	}
	cfg, err := config.Load(*path)
	return cfg, flags.Args(), err
}

func install(home string, stdout io.Writer) error {
	cfgPath := config.DefaultPath(home)
	cfg := config.Default(home)
	if existing, err := config.Load(cfgPath); err == nil {
		cfg = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("existing config is invalid: %w", err)
	}
	for _, dir := range []string{cfg.StateDir, cfg.QueueDir(), cfg.DoneDir(), cfg.FailedDir(), cfg.LogsDir(), cfg.WorkDir(), cfg.OutputDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := config.Write(cfgPath, cfg); err != nil {
		return err
	}

	currentExecutable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	installedBin := filepath.Join(cfg.StateDir, "bin", executableName)
	if err := os.MkdirAll(filepath.Dir(installedBin), 0o700); err != nil {
		return fmt.Errorf("create installed bin dir: %w", err)
	}
	if err := copyExecutable(currentExecutable, installedBin); err != nil {
		return err
	}

	luaPath := filepath.Join(home, "Library", "Application Support", "obs-studio", "scripts", "obs_interview_hook.lua")
	lua := strings.ReplaceAll(luaHookTemplate, "__PROCESSOR_PATH__", luaEscape(installedBin))
	lua = strings.ReplaceAll(lua, "__CONFIG_PATH__", luaEscape(cfgPath))
	if err := writeAtomic(luaPath, []byte(lua), 0o600); err != nil {
		return fmt.Errorf("install OBS hook: %w", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", config.LaunchAgentLabel+".plist")
	plist := launchAgentPlist(installedBin, cfgPath, cfg)
	if err := writeAtomic(plistPath, []byte(plist), 0o600); err != nil {
		return fmt.Errorf("install LaunchAgent: %w", err)
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("/bin/launchctl", "bootout", domain+"/"+config.LaunchAgentLabel).Run()
	if output, err := exec.Command("/bin/launchctl", "bootstrap", domain, plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("load LaunchAgent: %w: %s", err, strings.TrimSpace(string(output)))
	}
	_ = exec.Command("/bin/launchctl", "enable", domain+"/"+config.LaunchAgentLabel).Run()
	fmt.Fprintf(stdout, "installed binary: %s\nconfig: %s\nOBS hook: %s\nLaunchAgent: %s\n", installedBin, cfgPath, luaPath, plistPath)
	return nil
}

func doctor(ctx context.Context, cfg config.Config, stdout io.Writer) error {
	type check struct {
		Name   string `json:"name"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	var checks []check
	for name, path := range map[string]string{
		"ffmpeg":             cfg.FFmpegCommand,
		"ffprobe":            cfg.FFprobeCommand,
		"whisper-server":     cfg.WhisperServerCommand,
		"whisper-gate":       cfg.WhisperGateCommand,
		"whisper-model":      cfg.WhisperModelPath,
		"whisper-gate-model": cfg.WhisperGateModelPath,
	} {
		info, err := os.Stat(path)
		checks = append(checks, check{Name: name, OK: err == nil && !info.IsDir(), Detail: path})
	}
	encoderOutput, encoderErr := exec.CommandContext(ctx, cfg.FFmpegCommand, "-hide_banner", "-encoders").CombinedOutput()
	checks = append(checks, check{Name: "hevc_videotoolbox", OK: encoderErr == nil && strings.Contains(string(encoderOutput), "hevc_videotoolbox"), Detail: "Apple VideoToolbox HEVC encoder"})
	domain := fmt.Sprintf("gui/%d/%s", os.Getuid(), config.LaunchAgentLabel)
	launchOutput, launchErr := exec.CommandContext(ctx, "/bin/launchctl", "print", domain).CombinedOutput()
	checks = append(checks, check{Name: "launch-agent", OK: launchErr == nil, Detail: firstLine(string(launchOutput))})
	allOK := true
	for _, item := range checks {
		allOK = allOK && item.OK
	}
	printJSON(stdout, checks)
	if !allOK {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

func runQueue(ctx context.Context, cfg config.Config, stdout, stderr io.Writer) error {
	return jobqueue.WithLock(cfg, func() error {
		paths, err := jobqueue.Pending(cfg)
		if err != nil {
			return err
		}
		pipeline := processor.New(cfg)
		for _, path := range paths {
			job, readErr := jobqueue.Read(path)
			if readErr != nil {
				job = jobqueue.Job{ID: strings.TrimSuffix(filepath.Base(path), ".json"), Path: "<invalid>"}
				if finishErr := jobqueue.Finish(cfg, path, job, readErr); finishErr != nil {
					return errors.Join(readErr, finishErr)
				}
				fmt.Fprintf(stderr, "invalid job %s: %v\n", path, readErr)
				continue
			}
			fmt.Fprintf(stdout, "%s processing %s\n", time.Now().Format(time.RFC3339), job.Path)
			result, processErr := pipeline.Process(ctx, job.Path)
			if finishErr := jobqueue.Finish(cfg, path, job, processErr); finishErr != nil {
				return errors.Join(processErr, finishErr)
			}
			if processErr != nil {
				fmt.Fprintf(stderr, "%s failed %s: %v\n", time.Now().Format(time.RFC3339), job.Path, processErr)
				notify(cfg, "Ошибка обработки OBS", filepath.Base(job.Path)+": "+processErr.Error())
				continue
			}
			fmt.Fprintf(stdout, "%s completed %s -> %s (%d -> %d bytes)\n", time.Now().Format(time.RFC3339), job.Path, result.FinalDir, result.SourceBytes, result.OutputBytes)
			notify(cfg, "Запись OBS обработана", filepath.Base(job.Path)+" — видео и расшифровка готовы")
		}
		return nil
	})
}

func notify(cfg config.Config, title, message string) {
	if !cfg.Notifications {
		return
	}
	script := `on run argv
display notification (item 2 of argv) with title (item 1 of argv)
end run`
	_ = exec.Command("/usr/bin/osascript", "-e", script, title, message).Run()
}

func launchAgentPlist(binary, cfgPath string, cfg config.Config) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>run-queue</string>
    <string>--config</string>
    <string>%s</string>
  </array>
  <key>QueueDirectories</key>
  <array><string>%s</string></array>
  <key>ProcessType</key>
  <string>Background</string>
  <key>LowPriorityIO</key>
  <false/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, xmlEscape(config.LaunchAgentLabel), xmlEscape(binary), xmlEscape(cfgPath), xmlEscape(cfg.QueueDir()), xmlEscape(filepath.Join(cfg.LogsDir(), "processor.log")), xmlEscape(filepath.Join(cfg.LogsDir(), "processor-error.log")))
}

func copyExecutable(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open current executable: %w", err)
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".binary-*")
	if err != nil {
		return fmt.Errorf("create installed executable: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, input); err != nil {
		_ = temp.Close()
		return fmt.Errorf("copy executable: %w", err)
	}
	if err := temp.Chmod(0o755); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod executable: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync executable: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close executable: %w", err)
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("publish executable: %w", err)
	}
	return nil
}

func writeAtomic(path string, payload []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".install-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func usage(out io.Writer) {
	fmt.Fprintln(out, "usage: obs-interview-processor <install|doctor|enqueue|run-queue|process> [--config path] [recording]")
}

func printJSON(out io.Writer, value any) {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return value[:index]
	}
	return value
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}

func luaEscape(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "\"", "\\\"")
}
