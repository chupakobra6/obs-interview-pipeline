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

	"github.com/chupakobra6/obs-interview-pipeline/internal/asr"
	"github.com/chupakobra6/obs-interview-pipeline/internal/config"
	"github.com/chupakobra6/obs-interview-pipeline/internal/policy"
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
		cfg, options, rest, err := loadJobArgs(home, "enqueue", args[1:])
		if err != nil || len(rest) != 1 {
			if err == nil {
				err = fmt.Errorf("enqueue requires one recording path")
			}
			fmt.Fprintln(stderr, err)
			return 2
		}
		job, err := jobqueue.Enqueue(cfg, rest[0], options)
		if err != nil {
			fmt.Fprintf(stderr, "enqueue: %v\n", err)
			return 1
		}
		printJSON(stdout, job)
		return 0
	case "prompt":
		cfg, cfgPath, rest, err := loadConfigPathArgs(home, "prompt", args[1:])
		if err != nil || len(rest) != 1 {
			if err == nil {
				err = fmt.Errorf("prompt requires one recording path")
			}
			fmt.Fprintln(stderr, err)
			return 2
		}
		if err := launchPrompt(cfg, cfgPath, rest[0]); err != nil {
			fmt.Fprintf(stderr, "prompt: %v\n", err)
			return 1
		}
		return 0
	case "process":
		cfg, options, rest, err := loadJobArgs(home, "process", args[1:])
		if err != nil || len(rest) != 1 {
			if err == nil {
				err = fmt.Errorf("process requires one recording path")
			}
			fmt.Fprintln(stderr, err)
			return 2
		}
		result, err := processor.New(cfg).Process(ctx, rest[0], options)
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
	cfg, _, rest, err := loadConfigPathArgs(home, name, args)
	return cfg, rest, err
}

func loadConfigPathArgs(home, name string, args []string) (config.Config, string, []string, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", config.DefaultPath(home), "config path")
	if err := flags.Parse(args); err != nil {
		return config.Config{}, "", nil, err
	}
	cfg, err := config.Load(*path)
	return cfg, *path, flags.Args(), err
}

func loadJobArgs(home, name string, args []string) (config.Config, policy.Options, []string, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", config.DefaultPath(home), "config path")
	deleteSource := flags.Bool("delete-source", false, "delete source after successful validation")
	audioMode := flags.String("audio-mode", policy.AudioPreserve, "audio mode: preserve or merge")
	if err := flags.Parse(args); err != nil {
		return config.Config{}, policy.Options{}, nil, err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return config.Config{}, policy.Options{}, nil, err
	}
	deleteWasSet := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == "delete-source" {
			deleteWasSet = true
		}
	})
	if !deleteWasSet {
		*deleteSource = cfg.DeleteSourceOnSuccess
	}
	options := policy.Options{DeleteSource: *deleteSource, AudioMode: *audioMode}
	if err := options.Validate(); err != nil {
		return config.Config{}, policy.Options{}, nil, err
	}
	return cfg, options, flags.Args(), nil
}

func install(home string, stdout io.Writer) error {
	cfgPath := config.DefaultPath(home)
	cfg := config.Default(home)
	if existing, err := config.LoadForInstall(cfgPath, cfg); err == nil {
		cfg = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("existing config is invalid: %w", err)
	}
	for _, dir := range []string{cfg.StateDir, cfg.QueueDir(), cfg.DoneDir(), cfg.FailedDir(), cfg.LogsDir(), cfg.WorkDir(), cfg.OutputDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := installNotifier(cfg); err != nil {
		return err
	}
	if err := installPrompt(cfg); err != nil {
		return err
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
	toolChecks := []struct {
		name string
		path string
	}{
		{name: "ffmpeg", path: cfg.FFmpegCommand},
		{name: "ffprobe", path: cfg.FFprobeCommand},
		{name: "make", path: cfg.MakeCommand},
		{name: "telegram-harvest", path: cfg.TelegramHarvestCommand},
		{name: "obs-interview-notifier", path: cfg.NotifierCommand},
		{name: "obs-interview-prompt", path: cfg.PromptCommand},
		{name: "swiftc", path: "/usr/bin/swiftc"},
	}
	for _, tool := range toolChecks {
		info, err := os.Stat(tool.path)
		checks = append(checks, check{Name: tool.name, OK: err == nil && !info.IsDir(), Detail: tool.path})
	}
	rootInfo, rootErr := os.Stat(cfg.TelegramHarvestRoot)
	checks = append(checks, check{Name: "telegram-harvest-root", OK: rootErr == nil && rootInfo.IsDir(), Detail: cfg.TelegramHarvestRoot})
	notifierApp := applicationPath(cfg.NotifierCommand)
	signOutput, signErr := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", notifierApp).CombinedOutput()
	checks = append(checks, check{Name: "notifier-signature", OK: signErr == nil, Detail: oneLineDetail(string(signOutput))})
	promptApp := applicationPath(cfg.PromptCommand)
	promptSignOutput, promptSignErr := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", promptApp).CombinedOutput()
	checks = append(checks, check{Name: "prompt-signature", OK: promptSignErr == nil, Detail: oneLineDetail(string(promptSignOutput))})
	buildOutput, buildErr := exec.CommandContext(ctx, cfg.MakeCommand, "-s", "-C", cfg.TelegramHarvestRoot, "build").CombinedOutput()
	checks = append(checks, check{Name: "telegram-harvest-build", OK: buildErr == nil, Detail: oneLineDetail(string(buildOutput))})
	if buildErr == nil {
		asrCheck := exec.CommandContext(ctx, cfg.TelegramHarvestCommand, "--profile", "main", "transcribe-file", "--check")
		asrCheck.Dir = cfg.TelegramHarvestRoot
		asrOutput, asrErr := asrCheck.CombinedOutput()
		contractErr := asr.ValidateRuntimeCheckResponse(asrOutput)
		asrOK := asrErr == nil && contractErr == nil
		checks = append(checks, check{Name: "telegram-harvest-asr", OK: asrOK, Detail: oneLineDetail(string(asrOutput))})
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
			result, processErr := pipeline.Process(ctx, job.Path, job.Options)
			if finishErr := jobqueue.Finish(cfg, path, job, processErr); finishErr != nil {
				return errors.Join(processErr, finishErr)
			}
			if processErr != nil {
				fmt.Fprintf(stderr, "%s failed %s: %v\n", time.Now().Format(time.RFC3339), job.Path, processErr)
				if notifyErr := notify(cfg, "Ошибка обработки OBS", filepath.Base(job.Path)+": "+processErr.Error(), filepath.Dir(job.Path)); notifyErr != nil {
					fmt.Fprintf(stderr, "%s notification failed: %v\n", time.Now().Format(time.RFC3339), notifyErr)
				}
				continue
			}
			fmt.Fprintf(stdout, "%s completed %s -> %s (%d -> %d bytes)\n", time.Now().Format(time.RFC3339), job.Path, result.FinalDir, result.SourceBytes, result.OutputBytes)
			if notifyErr := notify(cfg, "Запись OBS обработана", filepath.Base(job.Path)+" — видео и расшифровка готовы", result.FinalDir); notifyErr != nil {
				fmt.Fprintf(stderr, "%s notification failed: %v\n", time.Now().Format(time.RFC3339), notifyErr)
			}
		}
		return nil
	})
}

func notify(cfg config.Config, title, message, targetDir string) error {
	if !cfg.Notifications {
		return nil
	}
	appPath := applicationPath(cfg.NotifierCommand)
	args := append([]string{"-n", appPath, "--args"}, notificationArgs(title, message, targetDir)...)
	if output, err := exec.Command("/usr/bin/open", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("launch notifier app: %w: %s", err, oneLineDetail(string(output)))
	}
	return nil
}

func notificationArgs(title, message, targetDir string) []string {
	return []string{"--title", title, "--message", message, "--open-dir", targetDir}
}

func applicationPath(command string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(command)))
}

func launchPrompt(cfg config.Config, cfgPath, recording string) error {
	absRecording, err := filepath.Abs(recording)
	if err != nil {
		return fmt.Errorf("resolve recording path: %w", err)
	}
	info, err := os.Stat(absRecording)
	if err != nil {
		return fmt.Errorf("stat recording: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return fmt.Errorf("recording must be a non-empty regular file")
	}
	installedProcessor := filepath.Join(cfg.StateDir, "bin", executableName)
	args := append([]string{"-n", applicationPath(cfg.PromptCommand), "--args"}, promptArgs(installedProcessor, cfgPath, absRecording, cfg.DeleteSourceOnSuccess)...)
	if output, err := exec.Command("/usr/bin/open", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("launch prompt app: %w: %s", err, oneLineDetail(string(output)))
	}
	return nil
}

func promptArgs(processorPath, cfgPath, recordingPath string, deleteSourceDefault bool) []string {
	return []string{
		"--processor", processorPath,
		"--config", cfgPath,
		"--recording", recordingPath,
		"--delete-source-default", strconv.FormatBool(deleteSourceDefault),
	}
}

func installNotifier(cfg config.Config) error {
	return installSwiftApp(swiftAppSpec{
		Command:      cfg.NotifierCommand,
		Executable:   "obs-interview-notifier",
		BundleID:     "com.igor.obs-interview-notifier",
		DisplayName:  "OBS Interview Notifier",
		SourceName:   "obs_interview_notifier.swift",
		Source:       notifierSwiftSource,
		Frameworks:   []string{"AppKit", "UserNotifications"},
		ErrorContext: "notifier",
	})
}

func installPrompt(cfg config.Config) error {
	return installSwiftApp(swiftAppSpec{
		Command:      cfg.PromptCommand,
		Executable:   "obs-interview-prompt",
		BundleID:     "com.igor.obs-interview-prompt",
		DisplayName:  "OBS Interview Prompt",
		SourceName:   "obs_interview_prompt.swift",
		Source:       promptSwiftSource,
		Frameworks:   []string{"AppKit"},
		ErrorContext: "prompt",
	})
}

type swiftAppSpec struct {
	Command      string
	Executable   string
	BundleID     string
	DisplayName  string
	SourceName   string
	Source       string
	Frameworks   []string
	ErrorContext string
}

func installSwiftApp(spec swiftAppSpec) error {
	macOSDir := filepath.Dir(spec.Command)
	contentsDir := filepath.Dir(macOSDir)
	appDir := filepath.Dir(contentsDir)
	resourcesDir := filepath.Join(contentsDir, "Resources")
	for _, dir := range []string{macOSDir, resourcesDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s app directory: %w", spec.ErrorContext, err)
		}
	}
	sourcePath := filepath.Join(resourcesDir, spec.SourceName)
	if err := writeAtomic(sourcePath, []byte(spec.Source), 0o600); err != nil {
		return fmt.Errorf("install %s source: %w", spec.ErrorContext, err)
	}
	infoPlist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>%s</string>
  <key>CFBundleIdentifier</key><string>%s</string>
  <key>CFBundleName</key><string>%s</string>
  <key>CFBundleDisplayName</key><string>%s</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSUIElement</key><true/>
</dict>
</plist>
`, xmlEscape(spec.Executable), xmlEscape(spec.BundleID), xmlEscape(spec.DisplayName), xmlEscape(spec.DisplayName))
	if err := writeAtomic(filepath.Join(contentsDir, "Info.plist"), []byte(infoPlist), 0o600); err != nil {
		return fmt.Errorf("install %s Info.plist: %w", spec.ErrorContext, err)
	}
	temporary, err := os.CreateTemp(macOSDir, "."+spec.Executable+"-*")
	if err != nil {
		return fmt.Errorf("create temporary %s binary: %w", spec.ErrorContext, err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary %s binary: %w", spec.ErrorContext, err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("prepare temporary %s binary: %w", spec.ErrorContext, err)
	}
	defer os.Remove(temporaryPath)
	compilerArgs := []string{"-swift-version", "5", "-O"}
	for _, framework := range spec.Frameworks {
		compilerArgs = append(compilerArgs, "-framework", framework)
	}
	compilerArgs = append(compilerArgs, sourcePath, "-o", temporaryPath)
	output, err := exec.Command("/usr/bin/swiftc", compilerArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("compile %s app: %w: %s", spec.ErrorContext, err, oneLineDetail(string(output)))
	}
	if err := os.Chmod(temporaryPath, 0o755); err != nil {
		return fmt.Errorf("chmod %s app: %w", spec.ErrorContext, err)
	}
	if err := os.Rename(temporaryPath, spec.Command); err != nil {
		return fmt.Errorf("publish %s app: %w", spec.ErrorContext, err)
	}
	signOutput, signErr := exec.Command(
		"/usr/bin/codesign",
		"--force",
		"--deep",
		"--sign", "-",
		"--identifier", spec.BundleID,
		appDir,
	).CombinedOutput()
	if signErr != nil {
		return fmt.Errorf("sign %s app: %w: %s", spec.ErrorContext, signErr, oneLineDetail(string(signOutput)))
	}
	return nil
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
  <string>Interactive</string>
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
	fmt.Fprintln(out, "usage: obs-interview-processor <install|doctor|prompt|enqueue|run-queue|process> [--config path] [--delete-source bool] [--audio-mode preserve|merge] [recording]")
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

func oneLineDetail(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 500 {
		return value[:500] + "..."
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
