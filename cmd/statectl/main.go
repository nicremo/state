package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nicremo/state/internal/statectl"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(os.Args[1:], os.Stdout, os.Stderr, logger); err != nil {
		logger.Error("statectl failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer, logger *slog.Logger) error {
	if len(args) == 0 {
		return errors.New("usage: statectl <pair|mcp|doctor|install|uninstall|unpair|version>")
	}
	switch args[0] {
	case "pair":
		return runPair(args[1:], stdout, stderr)
	case "mcp":
		return runMCP(args[1:], stderr)
	case "doctor", "diagnose":
		return runDoctor(args[1:], stdout, stderr)
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "uninstall":
		return runUninstall(args[1:], stdout, stderr)
	case "unpair":
		return runUnpair(args[1:], stdout, stderr)
	case "version":
		_, err := fmt.Fprintln(stdout, version)
		return err
	default:
		logger.Debug("unknown statectl command", "command", args[0])
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runPair(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl pair", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serverURL := flags.String("server", "", "State server base URL")
	code := flags.String("code", "", "one-time pairing code")
	harness := flags.String("harness", "", "codex, claude-code, or opencode")
	profileName := flags.String("profile", "", "local profile name, defaults to harness")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	install := flags.Bool("install", true, "install global MCP config and agent rules")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *profileName == "" {
		*profileName = *harness
	}
	configStore := statectl.NewConfigStore(*configPath)
	pairer := statectl.NewPairService(configStore, statectl.KeyringSecretStore{}, nil)
	profile, err := pairer.Pair(context.Background(), statectl.PairRequest{
		ProfileName: *profileName,
		ServerURL:   *serverURL,
		Code:        *code,
		Harness:     *harness,
	})
	if err != nil {
		return err
	}
	if *install {
		installer, err := defaultInstaller()
		if err != nil {
			return err
		}
		if err := installer.Install(*harness, profile.Name); err != nil {
			return fmt.Errorf("profile paired but harness installation failed: %w", err)
		}
	}
	_, err = fmt.Fprintf(stdout, "paired %s as %s\n", profile.Harness, profile.Name)
	return err
}

func runMCP(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	profile, token, err := loadProfileAndCredential(*configPath, *profileName)
	if err != nil {
		return err
	}
	remoteSession, err := connectRemote(context.Background(), profile, token)
	if err != nil {
		return err
	}
	defer remoteSession.Close()
	proxy, err := statectl.NewProxyServer(context.Background(), remoteSession, version)
	if err != nil {
		return err
	}
	return proxy.Run(context.Background(), &mcp.StdioTransport{})
}

func runDoctor(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	profile, token, err := loadProfileAndCredential(*configPath, *profileName)
	if err != nil {
		return err
	}
	serverVersion, err := fetchServerVersion(profile.ServerURL)
	if err != nil {
		return err
	}
	session, err := connectRemote(context.Background(), profile, token)
	if err != nil {
		return err
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	_, err = fmt.Fprintf(stdout, "server: %s\nprotocol: %s\nactor: %s\ntools: %d (%s)\n",
		serverVersion,
		session.InitializeResult().ProtocolVersion,
		profile.ActorID,
		len(names),
		strings.Join(names, ", "),
	)
	return err
}

func runInstall(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	harness := flags.String("harness", "", "codex, claude-code, or opencode")
	profileName := flags.String("profile", "", "statectl profile name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *profileName == "" {
		*profileName = *harness
	}
	installer, err := defaultInstaller()
	if err != nil {
		return err
	}
	if err := installer.Install(*harness, *profileName); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "installed State for %s\n", *harness)
	return err
}

func runUninstall(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl uninstall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	harness := flags.String("harness", "", "codex, claude-code, or opencode")
	if err := flags.Parse(args); err != nil {
		return err
	}
	installer, err := defaultInstaller()
	if err != nil {
		return err
	}
	if err := installer.Uninstall(*harness); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "uninstalled State from %s\n", *harness)
	return err
}

func runUnpair(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl unpair", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	removeIntegration := flags.Bool("uninstall", true, "remove harness configuration and rules")
	if err := flags.Parse(args); err != nil {
		return err
	}
	store := statectl.NewConfigStore(*configPath)
	profile, err := store.LoadProfile(*profileName)
	if err != nil {
		return err
	}
	if *removeIntegration {
		installer, err := defaultInstaller()
		if err != nil {
			return err
		}
		if err := installer.Uninstall(profile.Harness); err != nil {
			return err
		}
	}
	if err := (statectl.KeyringSecretStore{}).Delete(profile.CredentialAccount()); err != nil {
		return err
	}
	if err := store.RemoveProfile(profile.Name); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "removed local profile %s\n", profile.Name)
	return err
}

func loadProfileAndCredential(configPath string, profileName string) (statectl.Profile, string, error) {
	if profileName == "" {
		return statectl.Profile{}, "", errors.New("profile is required")
	}
	profile, err := statectl.NewConfigStore(configPath).LoadProfile(profileName)
	if err != nil {
		return statectl.Profile{}, "", err
	}
	token, err := (statectl.KeyringSecretStore{}).Get(profile.CredentialAccount())
	if err != nil {
		return statectl.Profile{}, "", err
	}
	return profile, token, nil
}

func connectRemote(ctx context.Context, profile statectl.Profile, token string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "statectl", Version: version}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             strings.TrimRight(profile.ServerURL, "/") + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}, nil)
}

func fetchServerVersion(serverURL string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(strings.TrimRight(serverURL, "/") + "/version")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("version endpoint returned status %d", response.StatusCode)
	}
	result := struct {
		Version string `json:"version"`
	}{}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return "", err
	}
	return result.Version, nil
}

func defaultInstaller() (*statectl.Installer, error) {
	paths, err := statectl.DefaultInstallPaths()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	return statectl.NewInstaller(paths, executable, time.Now), nil
}

func defaultConfigPath() string {
	path, err := statectl.DefaultConfigPath()
	if err != nil {
		return "statectl.json"
	}
	return path
}

type bearerRoundTripper struct {
	token string
}

func (transport bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return http.DefaultTransport.RoundTrip(clone)
}
