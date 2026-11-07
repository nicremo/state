package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nicremo/state/internal/state"
)

// T3 Code is a desktop harness with its own local server. Its CLI starts and
// continues threads on that server; the thread then shows up in the T3 Code
// app on the Mac and, through T3 Connect, on the owner's other devices.
const (
	t3AdapterSlug = "t3-code"
	// t3WaitingText is the round's answer when the thread stopped to ask the
	// owner something in T3 Code (CLI exit code 2).
	t3WaitingText = "Wartet in T3 Code auf deine Antwort."
	// t3RightsText explains why a policy below full rights is refused: T3
	// Code applies its own approval settings, not State's rights levels.
	t3RightsText = "T3 Code setzt die Rechte der Policy nicht durch. Gib der Policy volle Rechte."
	// t3NoProjectMarker is the CLI's message for a folder without a T3
	// project.
	t3NoProjectMarker = "No T3 Code project matches"
	// t3TitleLimit mirrors the CLI's own title bound.
	t3TitleLimit = 80
	// t3HelperTimeout bounds the short helper calls (project check, project
	// add, thread show). They take well under a second, but the first call
	// from a new runner binary can wait for macOS to assess the T3 Code app.
	t3HelperTimeout = 2 * time.Minute
)

// t3Adapter runs one agent round as a T3 Code thread: the first round
// starts a thread in the project folder, later rounds send the owner's
// message to the same thread. The thread ID is the round's harness session
// ID and the thread's T3 link its harness link.
type t3Adapter struct {
	// home overrides the home directory for the ~/.t3/bin fallback; tests
	// set it.
	home string
}

func (adapter *t3Adapter) Name() string {
	return t3AdapterSlug
}

// binary finds the t3 CLI. First the binary of T3's background service
// (~/.t3/runtime/versions/<active>/t3): it works without the desktop app,
// open or installed. Then t3 on PATH, then the launcher in ~/.t3/bin, which
// runs the desktop app's bundled CLI.
func (adapter *t3Adapter) binary() (string, error) {
	home := adapter.homeDir()
	if home != "" {
		if service := t3ServiceBinary(home); service != "" {
			return service, nil
		}
	}
	if path, err := exec.LookPath("t3"); err == nil {
		return path, nil
	}
	if home != "" {
		if candidate := filepath.Join(home, ".t3", "bin", "t3"); executableFile(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: t3 not found (T3 service, PATH or ~/.t3/bin), install T3 Code", ErrAdapterUnavailable)
}

func (adapter *t3Adapter) homeDir() string {
	if adapter.home != "" {
		return adapter.home
	}
	home, _ := os.UserHomeDir()
	return home
}

// t3ServiceBinary is the CLI of the T3 version the background service runs,
// as recorded in ~/.t3/runtime/service-state.json.
func t3ServiceBinary(home string) string {
	contents, err := os.ReadFile(filepath.Join(home, ".t3", "runtime", "service-state.json"))
	if err != nil {
		return ""
	}
	var serviceState struct {
		ActiveVersion string `json:"activeVersion"`
	}
	if json.Unmarshal(contents, &serviceState) != nil || serviceState.ActiveVersion == "" || strings.ContainsAny(serviceState.ActiveVersion, `/\`) || serviceState.ActiveVersion == ".." {
		return ""
	}
	candidate := filepath.Join(home, ".t3", "runtime", "versions", serviceState.ActiveVersion, "t3")
	if !executableFile(candidate) {
		return ""
	}
	return candidate
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func (adapter *t3Adapter) Validate(contract state.TaskContract) error {
	if _, err := adapter.binary(); err != nil {
		return err
	}
	if rightsFor(contract.AllowedCapabilities) != rightsFull {
		return fmt.Errorf("%w: %s", ErrAdapterUnavailable, t3RightsText)
	}
	return nil
}

func (adapter *t3Adapter) Start(ctx context.Context, request StartRequest) (Session, error) {
	if err := adapter.Validate(request.Contract); err != nil {
		return nil, err
	}
	binary, _ := adapter.binary()
	threadID := request.Contract.ResumeSessionID
	var args []string
	if threadID == "" {
		model, err := ensureT3Project(ctx, binary, request.Dir)
		if err != nil {
			return nil, err
		}
		args = []string{"thread", "start", "--wait", "--title", t3Title(request.Contract.Objective)}
		// The model the owner chose wins; without one the project's newest
		// thread shows what currently works in T3.
		if chosen := (state.AgentModel{Provider: request.Contract.ModelProvider, Model: request.Contract.Model}); chosen.Valid() {
			model = t3Model{provider: chosen.Provider, model: chosen.Model}
		}
		if model.provider != "" && model.model != "" {
			args = append(args, "--provider", model.provider, "--model", model.model)
		}
		args = append(args, request.Dir, "-")
	} else {
		if !state.ValidHarnessSessionID(threadID) {
			return nil, errors.New("invalid T3 thread ID")
		}
		args = []string{"thread", "send", "--wait", threadID, "-"}
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = request.Dir
	command.Stdin = strings.NewReader(request.Prompt)
	tail := &tailBuffer{limit: OutputTailLimit}
	stdout := &tailBuffer{limit: agentOutputLimit}
	stderr := &t3StderrWatcher{threadID: threadID}
	command.Stdout = io.MultiWriter(tail, stdout)
	command.Stderr = io.MultiWriter(tail, stderr)
	prepareProcessGroup(command)
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start t3: %w", err)
	}
	return &t3Session{binary: binary, command: command, tail: tail, stdout: stdout, stderr: stderr}, nil
}

// t3Model is the provider instance and model a new thread runs with.
type t3Model struct {
	provider string
	model    string
}

// ensureT3Project registers the folder as a T3 project when T3 knows none
// for it, so a new State project needs no setup in T3 Code. It returns the
// provider and model of the project's newest thread: that is the setup the
// owner works with in T3, while the project's stored default can point to a
// provider instance that was disabled since. Without threads the project
// default applies.
func ensureT3Project(ctx context.Context, binary string, dir string) (t3Model, error) {
	output, err := runT3Helper(ctx, binary, dir, "thread", "list", "--json", "--project", dir)
	if err == nil {
		return newestT3Model(output), nil
	}
	if !strings.Contains(output, t3NoProjectMarker) {
		return t3Model{}, fmt.Errorf("%w: T3 Code did not answer (%s), is the app running? %s", ErrAdapterUnavailable, t3Failure(ctx, err), lastLine(output))
	}
	if output, err := runT3Helper(ctx, binary, dir, "project", "add", dir); err != nil {
		return t3Model{}, fmt.Errorf("%w: add the folder to T3 Code: %s", ErrAdapterUnavailable, lastLine(output))
	}
	return t3Model{}, nil
}

// newestT3Model reads `t3 thread list --json` (newest first) and returns the
// first thread's provider and model that are both set.
func newestT3Model(output string) t3Model {
	var threads []struct {
		ProviderInstanceID string `json:"providerInstanceId"`
		Model              string `json:"model"`
	}
	start := strings.Index(output, "[")
	if start < 0 || json.Unmarshal([]byte(output[start:]), &threads) != nil {
		return t3Model{}
	}
	for _, thread := range threads {
		if thread.ProviderInstanceID != "" && thread.Model != "" {
			return t3Model{provider: thread.ProviderInstanceID, model: thread.Model}
		}
	}
	return t3Model{}
}

// t3Failure names why a helper call failed: its own deadline or the CLI's
// exit status.
func t3Failure(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || (ctx.Err() == nil && strings.Contains(err.Error(), "killed")) {
		return fmt.Sprintf("no answer within %s", t3HelperTimeout)
	}
	return err.Error()
}

// runT3Helper runs a short t3 call with its own deadline and returns the
// combined output.
func runT3Helper(ctx context.Context, binary string, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, t3HelperTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("%w: %v", ctx.Err(), err)
	}
	return string(output), err
}

// t3Title is the thread title: the objective's first line, bounded like the
// CLI bounds it.
func t3Title(objective string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(objective), "\n", 2)[0])
	if line == "" {
		return "State"
	}
	if utf8.RuneCountInString(line) <= t3TitleLimit {
		return line
	}
	runes := []rune(line)
	return strings.TrimSpace(string(runes[:t3TitleLimit-1])) + "…"
}

// t3StderrWatcher keeps the CLI's error output and picks up the thread ID
// from the "Thread <id>" line the CLI writes as soon as the thread exists.
type t3StderrWatcher struct {
	mutex    sync.Mutex
	threadID string
	data     []byte
}

func (watcher *t3StderrWatcher) Write(chunk []byte) (int, error) {
	watcher.mutex.Lock()
	defer watcher.mutex.Unlock()
	watcher.data = append(watcher.data, chunk...)
	if len(watcher.data) > OutputTailLimit {
		watcher.data = append([]byte(nil), watcher.data[len(watcher.data)-OutputTailLimit:]...)
	}
	if watcher.threadID == "" {
		scanner := bufio.NewScanner(bytes.NewReader(watcher.data))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) == 2 && fields[0] == "Thread" && state.ValidHarnessSessionID(fields[1]) {
				watcher.threadID = fields[1]
				break
			}
		}
	}
	return len(chunk), nil
}

func (watcher *t3StderrWatcher) result() (string, string) {
	watcher.mutex.Lock()
	defer watcher.mutex.Unlock()
	return watcher.threadID, string(watcher.data)
}

// t3Session is one running t3 thread call.
type t3Session struct {
	binary  string
	command *exec.Cmd
	tail    *tailBuffer
	stdout  *tailBuffer
	stderr  *t3StderrWatcher
}

func (session *t3Session) Wait(ctx context.Context) (Result, error) {
	err := session.command.Wait()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() >= 0 {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}
	threadID, errorOutput := session.stderr.result()
	result := Result{ExitCode: exitCode, Tail: session.tail.String(), HarnessSessionID: threadID}
	switch exitCode {
	case 0:
		result.Text = strings.TrimSpace(session.stdout.String())
	case 2:
		// The thread asks the owner something in T3 Code. Nothing failed;
		// the round is handed over and State's next message queues there.
		result.ExitCode = 0
		result.Text = t3WaitingText
	default:
		result.Text = t3ErrorText(errorOutput, threadID)
	}
	if threadID != "" {
		// A fresh context: the round's context may already be cancelled,
		// and the link is what lets the owner follow the thread anyway.
		result.HarnessLink = t3ThreadLink(context.WithoutCancel(ctx), session.binary, session.command.Dir, threadID)
	}
	return result, nil
}

func (session *t3Session) Cancel(_ context.Context) error {
	return killProcessGroup(session.command)
}

// t3ErrorText is the last meaningful stderr line, without the "Thread <id>"
// announcement.
func t3ErrorText(stderr string, threadID string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" || (threadID != "" && line == "Thread "+threadID) {
			continue
		}
		return line
	}
	return "T3 Code hat die Runde ohne Antwort beendet."
}

// t3ThreadLink reads the thread's T3 link; an unreadable or unsafe link is
// left out rather than failing the round.
func t3ThreadLink(ctx context.Context, binary string, dir string, threadID string) string {
	output, err := runT3Helper(ctx, binary, dir, "thread", "show", "--json", "--messages", "1", threadID)
	if err != nil {
		return ""
	}
	var shown struct {
		Thread struct {
			Link string `json:"link"`
		} `json:"thread"`
	}
	if json.Unmarshal([]byte(lastJSONLine(output)), &shown) != nil || !state.ValidHarnessLink(shown.Thread.Link) {
		return ""
	}
	return shown.Thread.Link
}

// lastJSONLine skips log lines a CLI may print before its JSON.
func lastJSONLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.HasPrefix(strings.TrimSpace(lines[index]), "{") {
			return lines[index]
		}
	}
	return ""
}

func lastLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
