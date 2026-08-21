package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nicremo/state/internal/api"
	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/mcpserver"
	statepush "github.com/nicremo/state/internal/push"
	"github.com/nicremo/state/internal/securefile"
	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/store"
	"github.com/pocketbase/pocketbase"
)

var version = "dev"

type applicationConfig struct {
	dataDirectory string
	version       string
}

type application struct {
	handler    http.Handler
	pocketBase *pocketbase.PocketBase
	repository *store.PocketBaseRepository
	push       *statepush.Service
	state      *state.Service
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(os.Args[1:], os.Stdout, os.Stderr, logger); err != nil {
		logger.Error("state-server stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer, logger *slog.Logger) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	switch command {
	case "serve":
		return runServe(args, stderr, logger)
	case "bootstrap-token":
		return runBootstrapToken(args, stdout, stderr)
	case "verify-audit":
		return runVerifyAudit(args, stdout, stderr)
	case "version":
		_, err := fmt.Fprintln(stdout, version)
		return err
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func runServe(args []string, stderr io.Writer, logger *slog.Logger) error {
	flags := flag.NewFlagSet("state-server serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDirectory := flags.String("data", environmentOrDefault("STATE_DATA_DIR", "/data"), "persistent data directory")
	httpAddress := flags.String("http", environmentOrDefault("STATE_HTTP_ADDR", "0.0.0.0:8090"), "HTTP listen address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	app, err := newApplication(applicationConfig{dataDirectory: *dataDirectory, version: version})
	if err != nil {
		return err
	}
	defer app.close()

	server := &http.Server{
		Addr:              *httpAddress,
		Handler:           app.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go runPushScheduler(ctx, app.push, logger)
	go runExecutionScheduler(ctx, app.state, logger)
	serverError := make(chan error, 1)
	go func() {
		logger.Info("state-server listening", "address", *httpAddress, "version", version)
		serverError <- server.ListenAndServe()
	}()

	select {
	case err := <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	}
}

func runBootstrapToken(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("state-server bootstrap-token", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDirectory := flags.String("data", environmentOrDefault("STATE_DATA_DIR", "/data"), "persistent data directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	token, err := securefile.LoadOrCreateBootstrapToken(filepath.Join(*dataDirectory, "state_secrets", "bootstrap.token"))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, token)
	return err
}

func runVerifyAudit(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("state-server verify-audit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDirectory := flags.String("data", environmentOrDefault("STATE_DATA_DIR", "/data"), "persistent data directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	app, err := newApplication(applicationConfig{dataDirectory: *dataDirectory, version: version})
	if err != nil {
		return err
	}
	defer app.close()
	if err := app.repository.VerifyAuditChain(context.Background()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "audit chain verified")
	return err
}

func newApplication(config applicationConfig) (*application, error) {
	if config.dataDirectory == "" {
		return nil, errors.New("data directory is empty")
	}
	secretDirectory := filepath.Join(config.dataDirectory, "state_secrets")
	signingKey, err := securefile.LoadOrCreateAuditSigningKey(filepath.Join(secretDirectory, "audit-signing.key"))
	if err != nil {
		return nil, fmt.Errorf("load audit signing key: %w", err)
	}
	bootstrapToken, err := securefile.LoadOrCreateBootstrapToken(filepath.Join(secretDirectory, "bootstrap.token"))
	if err != nil {
		return nil, fmt.Errorf("load bootstrap token: %w", err)
	}
	pb := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  config.dataDirectory,
		HideStartBanner: true,
	})
	if err := pb.Bootstrap(); err != nil {
		return nil, fmt.Errorf("bootstrap PocketBase: %w", err)
	}
	repository, err := store.NewPocketBaseRepository(pb, signingKey)
	if err != nil {
		_ = pb.ResetBootstrapState()
		return nil, err
	}
	authManager, err := stateauth.NewManager(pb, bootstrapToken)
	if err != nil {
		_ = pb.ResetBootstrapState()
		return nil, err
	}
	pushKey, err := securefile.LoadOrCreateEncryptionKey(filepath.Join(secretDirectory, "push-encryption.key"))
	if err != nil {
		_ = pb.ResetBootstrapState()
		return nil, fmt.Errorf("load push encryption key: %w", err)
	}
	pushRepository, err := statepush.NewRepository(pb, pushKey)
	if err != nil {
		_ = pb.ResetBootstrapState()
		return nil, err
	}
	pushService := statepush.NewService(pushRepository, statepush.NewHTTPSender(nil))
	stateService := state.NewService(repository, state.WithRunNotifier(pushService.NotifyRunFinished))
	restHandler := api.NewHandler(api.Config{
		Auth:    authManager,
		State:   stateService,
		Push:    pushService,
		Version: config.version,
	})
	mcpHandler := mcpserver.NewHandler(mcpserver.Config{
		Auth:    authManager,
		State:   stateService,
		Push:    pushService,
		Version: config.version,
	})
	handler := http.NewServeMux()
	handler.Handle("/mcp", mcpHandler)
	handler.Handle("/", restHandler)
	return &application{
		handler:    handler,
		pocketBase: pb,
		repository: repository,
		push:       pushService,
		state:      stateService,
	}, nil
}

func runPushScheduler(ctx context.Context, service *statepush.Service, logger *slog.Logger) {
	if service == nil {
		return
	}
	run := func() {
		now := time.Now().UTC()
		delivered, err := service.DeliverDue(ctx, now.Add(-24*time.Hour), now)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Warn("push scheduler cycle failed", "error", err)
		}
		if delivered > 0 {
			logger.Info("push scheduler delivered notifications", "count", delivered)
		}
	}
	run()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// runExecutionScheduler materializes eligible runs from due occurrences,
// requeues expired leases, and retires stale runs — immediately at startup and
// then every 30 seconds, mirroring the push scheduler's discipline.
func runExecutionScheduler(ctx context.Context, service *state.Service, logger *slog.Logger) {
	if service == nil {
		return
	}
	run := func() {
		runExecutionCycle(ctx, service, logger, time.Now().UTC())
	}
	run()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func runExecutionCycle(ctx context.Context, service *state.Service, logger *slog.Logger, now time.Time) {
	materialized, err := service.MaterializeEligibleRuns(ctx, now)
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Warn("execution scheduler materialization failed", "error", err)
	}
	if len(materialized) > 0 {
		logger.Info("execution scheduler materialized runs", "count", len(materialized))
	}
	requeued, err := service.RequeueExpiredLeases(ctx, now)
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Warn("execution scheduler lease requeue failed", "error", err)
	}
	if len(requeued) > 0 {
		logger.Info("execution scheduler requeued runs", "count", len(requeued))
	}
	expired, err := service.ExpireStaleRuns(ctx, now)
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Warn("execution scheduler expiry failed", "error", err)
	}
	if len(expired) > 0 {
		logger.Info("execution scheduler expired runs", "count", len(expired))
	}
}

func (app *application) close() error {
	if app == nil || app.pocketBase == nil {
		return nil
	}
	return app.pocketBase.ResetBootstrapState()
}

func environmentOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
