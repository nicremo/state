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
	"strconv"
	"syscall"
	"time"

	"github.com/nicremo/state/internal/relay"
	"github.com/nicremo/state/internal/securefile"
)

var version = "dev"

type relayConfig struct {
	dataDirectory      string
	appID              string
	allowDevelopment   bool
	dryRunAPNS         bool
	apnsTeamID         string
	apnsKeyID          string
	apnsTopic          string
	apnsPrivateKeyPath string
	version            string
}

type relayApplication struct {
	handler    http.Handler
	repository *relay.SQLiteRepository
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(os.Args[1:], os.Stdout, os.Stderr, logger); err != nil {
		logger.Error("state-relay stopped", "error", err)
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
	case "version":
		_, err := fmt.Fprintln(stdout, version)
		return err
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func runServe(args []string, stderr io.Writer, logger *slog.Logger) error {
	flags := flag.NewFlagSet("state-relay serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDirectory := flags.String("data", environmentOrDefault("STATE_RELAY_DATA_DIR", "/data"), "persistent data directory")
	httpAddress := flags.String("http", environmentOrDefault("STATE_RELAY_HTTP_ADDR", "0.0.0.0:8091"), "HTTP listen address")
	appID := flags.String("app-id", environmentOrDefault("STATE_RELAY_APP_ID", "5DKU7FFK4X.com.fabincrm.state"), "App Attest application ID")
	allowDevelopment := flags.Bool("allow-development-attest", environmentBoolean("STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST", false), "allow development App Attest credentials")
	dryRunAPNS := flags.Bool("dry-run-apns", environmentBoolean("STATE_RELAY_DRY_RUN_APNS", false), "accept notifications without sending them to APNs")
	apnsTeamID := flags.String("apns-team-id", os.Getenv("STATE_RELAY_APNS_TEAM_ID"), "APNs team ID")
	apnsKeyID := flags.String("apns-key-id", os.Getenv("STATE_RELAY_APNS_KEY_ID"), "APNs key ID")
	apnsTopic := flags.String("apns-topic", environmentOrDefault("STATE_RELAY_APNS_TOPIC", "com.fabincrm.state"), "APNs topic")
	apnsPrivateKeyPath := flags.String("apns-private-key", os.Getenv("STATE_RELAY_APNS_PRIVATE_KEY_FILE"), "APNs private key file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	application, err := newRelayApplication(relayConfig{
		dataDirectory:      *dataDirectory,
		appID:              *appID,
		allowDevelopment:   *allowDevelopment,
		dryRunAPNS:         *dryRunAPNS,
		apnsTeamID:         *apnsTeamID,
		apnsKeyID:          *apnsKeyID,
		apnsTopic:          *apnsTopic,
		apnsPrivateKeyPath: *apnsPrivateKeyPath,
		version:            version,
	})
	if err != nil {
		return err
	}
	defer application.close()
	if *dryRunAPNS {
		logger.Warn("APNs dry run mode is enabled")
	}
	server := &http.Server{
		Addr:              *httpAddress,
		Handler:           application.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serverError := make(chan error, 1)
	go func() {
		logger.Info("state-relay listening", "address", *httpAddress, "version", version)
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

func newRelayApplication(config relayConfig) (*relayApplication, error) {
	if config.dataDirectory == "" || config.appID == "" {
		return nil, errors.New("relay data directory and application ID are required")
	}
	vaultKey, err := securefile.LoadOrCreateEncryptionKey(filepath.Join(config.dataDirectory, "state_secrets", "relay-encryption.key"))
	if err != nil {
		return nil, fmt.Errorf("load relay encryption key: %w", err)
	}
	repository, err := relay.OpenSQLiteRepository(filepath.Join(config.dataDirectory, "relay.db"), vaultKey)
	if err != nil {
		return nil, err
	}
	attestor, err := relay.NewAppleAttestor(relay.AppleAttestorConfig{
		AppID:            config.appID,
		AllowDevelopment: config.allowDevelopment,
	})
	if err != nil {
		_ = repository.Close()
		return nil, err
	}
	var dispatcher relay.Dispatcher
	if config.dryRunAPNS {
		dispatcher = dryRunDispatcher{}
	} else {
		if config.apnsTeamID == "" || config.apnsKeyID == "" || config.apnsTopic == "" || config.apnsPrivateKeyPath == "" {
			_ = repository.Close()
			return nil, errors.New("APNs credentials are required unless dry run mode is enabled")
		}
		privateKeyContents, err := os.ReadFile(config.apnsPrivateKeyPath)
		if err != nil {
			_ = repository.Close()
			return nil, fmt.Errorf("read APNs private key: %w", err)
		}
		privateKey, err := relay.ParseAPNSPrivateKey(privateKeyContents)
		if err != nil {
			_ = repository.Close()
			return nil, err
		}
		dispatcher, err = relay.NewAPNSDispatcher(relay.APNSConfig{
			TeamID:     config.apnsTeamID,
			KeyID:      config.apnsKeyID,
			Topic:      config.apnsTopic,
			PrivateKey: privateKey,
		})
		if err != nil {
			_ = repository.Close()
			return nil, err
		}
	}
	handler := relay.NewHandler(relay.Config{
		Repository: repository,
		Attestor:   attestor,
		Dispatcher: dispatcher,
		Limiter:    relay.NewTokenBucketLimiter(120, time.Minute, time.Now),
		Version:    config.version,
	})
	return &relayApplication{handler: handler, repository: repository}, nil
}

func (application *relayApplication) close() error {
	if application == nil || application.repository == nil {
		return nil
	}
	return application.repository.Close()
}

type dryRunDispatcher struct{}

func (dryRunDispatcher) Send(context.Context, relay.Notification) error { return nil }

func environmentOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func environmentBoolean(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
