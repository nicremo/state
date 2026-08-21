package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"

	"github.com/nicremo/state/internal/runner"
	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/statectl"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(os.Args[1:], os.Stdout, os.Stderr, logger); err != nil {
		logger.Error("state-runner failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer, logger *slog.Logger) error {
	if len(args) == 0 {
		return errors.New("usage: state-runner <pair|run|version>")
	}
	switch args[0] {
	case "pair":
		return runPair(args[1:], stdout, stderr)
	case "run":
		return runLoop(args[1:], stdout, stderr)
	case "version":
		_, err := fmt.Fprintln(stdout, version)
		return err
	default:
		logger.Debug("unknown state-runner command", "command", args[0])
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runPair(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("state-runner pair", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serverURL := flags.String("server", "", "State server base URL")
	code := flags.String("code", "", "one-time pairing code for a runner credential")
	name := flags.String("name", "", "runner display name, for example mac-mini")
	projects := flags.String("projects", "", "comma-separated project IDs this runner serves (owner can edit later)")
	adapters := flags.String("adapters", "", "comma-separated harness adapters this runner launches, for example codex,claude-code")
	workRoot := flags.String("work-root", "", "directory holding the project checkouts this runner works in")
	configPath := flags.String("config", defaultConfigPath(), "state-runner config path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *serverURL == "" || *code == "" || *name == "" || *workRoot == "" {
		return errors.New("state-runner pair requires --server, --code, --name and --work-root")
	}
	config := runner.RunnerConfig{
		ServerURL: strings.TrimRight(strings.TrimSpace(*serverURL), "/"),
		Name:      strings.TrimSpace(*name),
		Projects:  splitList(*projects),
		Adapters:  splitList(*adapters),
		WorkRoot:  *workRoot,
	}
	if err := config.Validate(); err != nil {
		return err
	}
	for _, adapter := range config.Adapters {
		if !state.ValidHarness(adapter) {
			return fmt.Errorf("adapter %q is not a valid harness slug", adapter)
		}
	}

	ctx := context.Background()
	anonymous := runner.NewClient(config.ServerURL, "", nil)
	credential, err := anonymous.ExchangePairingCode(ctx, *code)
	if err != nil {
		return err
	}
	if credential.Token == "" || credential.Actor.Kind != state.ActorKindRunner {
		return errors.New("pairing response is not a runner credential — create the code with kind runner (iOS Settings → Runners)")
	}
	client := runner.NewClient(config.ServerURL, credential.Token, nil)
	registered, err := client.Register(ctx, state.RegisterRunnerInput{
		DisplayName:     config.Name,
		Projects:        config.Projects,
		Adapters:        config.Adapters,
		ClientRequestID: uuid.NewString(),
		Source:          "state-runner",
	})
	if err != nil {
		return fmt.Errorf("register runner: %w", err)
	}
	if err := (statectl.KeyringSecretStore{}).Set(config.CredentialAccount(), credential.Token); err != nil {
		return fmt.Errorf("store runner credential in operating system keychain: %w", err)
	}
	if err := runner.SaveRunnerConfig(*configPath, config); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "paired runner %s (%s) with %d project(s), %d adapter(s)\n", registered.DisplayName, registered.ID, len(registered.Projects), len(registered.Adapters))
	return err
}

func runLoop(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("state-runner run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	once := flags.Bool("once", false, "perform one claim attempt cycle and exit (for tests and CI)")
	configPath := flags.String("config", defaultConfigPath(), "state-runner config path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := runner.LoadRunnerConfig(*configPath)
	if err != nil {
		return err
	}
	token, err := (statectl.KeyringSecretStore{}).Get(config.CredentialAccount())
	if err != nil {
		return fmt.Errorf("load runner credential: %w — re-run state-runner pair", err)
	}
	process := &runner.Runner{
		Config:   config,
		Client:   runner.NewClient(config.ServerURL, token, nil),
		Adapters: runner.DefaultAdapters(),
		Log:      stderr,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return process.Run(ctx, *once)
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func defaultConfigPath() string {
	path, err := runner.DefaultConfigPath()
	if err != nil {
		return "runner.json"
	}
	return path
}
