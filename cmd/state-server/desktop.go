package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/state"
)

type desktopRequest struct {
	Action  string `json:"action"`
	Harness string `json:"harness,omitempty"`
}

type desktopStatus struct {
	Type        string                  `json:"type"`
	ServerURL   string                  `json:"server_url"`
	LocalURL    string                  `json:"local_url"`
	Fingerprint string                  `json:"fingerprint"`
	Devices     []stateauth.ActorRecord `json:"devices"`
	Version     string                  `json:"version"`
	Pairing     *desktopPairing         `json:"pairing,omitempty"`
	Error       string                  `json:"error,omitempty"`
}

type desktopPairing struct {
	URL       string    `json:"url"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	Harness   string    `json:"harness,omitempty"`
}

// runDesktop communicates exclusively through inherited pipes. Pairing codes
// and local owner operations are never available on a network control endpoint.
func runDesktop(args []string, input io.Reader, output, stderr io.Writer, logger *slog.Logger) error {
	flags := flag.NewFlagSet("state-server desktop", flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data", "", "private desktop data directory")
	address := flags.String("https", "0.0.0.0:9847", "local network HTTPS address")
	localAddress := flags.String("local-http", "127.0.0.1:9848", "loopback-only API for local tools")
	host := flags.String("host", "", "Mac Bonjour hostname ending in .local")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *data == "" || !validDesktopHost(*host) {
		return errors.New("desktop requires a data directory and a valid .local hostname")
	}
	localHost, _, err := net.SplitHostPort(*localAddress)
	if err != nil || net.ParseIP(localHost) == nil || !net.ParseIP(localHost).IsLoopback() {
		return errors.New("local HTTP listener must use a loopback IP address")
	}
	if err := os.MkdirAll(*data, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(*data, 0o700); err != nil {
		return err
	}
	// Reserve both ports before opening the database, avoiding duplicate servers.
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return fmt.Errorf("local HTTPS port unavailable: %w", err)
	}
	defer listener.Close()
	localListener, err := net.Listen("tcp", *localAddress)
	if err != nil {
		return fmt.Errorf("local API port unavailable: %w", err)
	}
	defer localListener.Close()
	certificate, fingerprint, err := desktopCertificate(filepath.Join(*data, "desktop-identity.pem"), *host)
	if err != nil {
		return err
	}
	app, err := newApplication(applicationConfig{dataDirectory: *data, version: version})
	if err != nil {
		return err
	}
	defer app.close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	owner, err := app.auth.DesktopOwner(ctx)
	if err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	serverURL := "https://" + net.JoinHostPort(*host, port)
	localURL := "http://" + localListener.Addr().String()
	server := &http.Server{Handler: desktopLANOnly(app.handler), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10}
	localServer := &http.Server{Handler: app.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10}
	serverErrors := make(chan error, 2)
	go func() {
		serverErrors <- server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}))
	}()
	go func() { serverErrors <- localServer.Serve(localListener) }()
	logger.Info("state-server desktop listening", "address", serverURL, "local_address", localURL, "version", version)
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
		_ = localServer.Shutdown(shutdown)
	}()
	go runPushScheduler(ctx, app.push, logger)
	go runExecutionScheduler(ctx, app.state, logger)
	requests := make(chan desktopRequest)
	go func() {
		defer cancel() // A closed parent pipe also stops the child after an app crash.
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			var request desktopRequest
			if json.Unmarshal(scanner.Bytes(), &request) != nil {
				continue
			}
			select {
			case requests <- request:
			case <-ctx.Done():
				return
			}
		}
	}()
	encoder := json.NewEncoder(output)
	var pairing *desktopPairing
	lastDeviceCount := 0
	emit := func(message string) error {
		if pairing != nil {
			available, err := app.auth.DesktopPairingAvailable(ctx, owner, pairing.Code)
			if err != nil {
				return err
			}
			if !available {
				pairing = nil
			}
		}
		devices, err := app.auth.ListActors(ctx, owner, state.ActorKindDevice)
		if err != nil {
			return err
		}
		active := make([]stateauth.ActorRecord, 0)
		for _, device := range devices {
			if device.Actor.Kind == state.ActorKindDevice && device.RevokedAt == nil {
				active = append(active, device)
			}
		}
		if pairing != nil && (time.Now().After(pairing.ExpiresAt) || (pairing.Harness == "" && len(active) > lastDeviceCount)) {
			pairing = nil
		}
		lastDeviceCount = len(active)
		return encoder.Encode(desktopStatus{Type: "status", ServerURL: serverURL, LocalURL: localURL, Fingerprint: fingerprint, Devices: active, Version: version, Pairing: pairing, Error: message})
	}
	if err := emit(""); err != nil {
		return err
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-serverErrors:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ticker.C:
			if err := emit(""); err != nil {
				return err
			}
		case request := <-requests:
			switch request.Action {
			case "stop":
				return nil
			case "status":
			case "pair":
				// Reuse unexpired codes so repeated UI actions cannot flood the table.
				if pairing == nil || pairing.Harness != request.Harness || time.Now().After(pairing.ExpiresAt) {
					pairRequest := stateauth.PairingCodeRequest{Kind: state.ActorKindDevice, DisplayName: "iPhone", DeviceName: "iPhone"}
					if request.Harness != "" {
						pairRequest = stateauth.PairingCodeRequest{Kind: state.ActorKindHarness, Harness: request.Harness, DisplayName: request.Harness}
					}
					code, err := app.auth.CreatePairingCode(ctx, owner, pairRequest)
					if err != nil {
						if err := emit("Kopplung konnte nicht erstellt werden."); err != nil {
							return err
						}
						continue
					}
					values := url.Values{"server": {serverURL}, "code": {code.Code}, "fingerprint": {fingerprint}}
					pairing = &desktopPairing{URL: "state://pair?" + values.Encode(), Code: code.Code, ExpiresAt: code.ExpiresAt, Harness: request.Harness}
				}
			default:
				if err := emit("Unbekannte Aktion."); err != nil {
					return err
				}
				continue
			}
			if err := emit(""); err != nil {
				return err
			}
		}
	}
}

func validDesktopHost(host string) bool {
	if !strings.HasSuffix(host, ".local") || len(host) > 253 {
		return false
	}
	label := strings.TrimSuffix(host, ".local")
	if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return false
	}
	for _, c := range label {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func desktopLANOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := netip.ParseAddrPort(r.RemoteAddr)
		// Preserve IPv6 interface zones and normalize IPv4-mapped addresses.
		ip := peer.Addr().Unmap()
		if err != nil || !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
			http.Error(w, "local network only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func desktopCertificate(path, host string) (tls.Certificate, string, error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return tls.Certificate{}, "", errors.New("desktop identity must be a private regular file")
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		cert, err := tls.X509KeyPair(contents, contents)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		if certificateCoversHost(cert, host) {
			return cert, certificateFingerprint(cert), nil
		}
		// The stored identity was issued for another Bonjour name, for example
		// after the Mac was renamed. Replacing it changes the fingerprint, so
		// paired iPhones have to pair again.
		if err := os.Remove(path); err != nil {
			return tls.Certificate{}, "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "State Local Server"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		DNSNames: []string{host, "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	contents := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})...)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	_, writeErr := file.Write(contents)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := tls.X509KeyPair(contents, contents)
	return cert, certificateFingerprint(cert), err
}

func certificateCoversHost(cert tls.Certificate, host string) bool {
	if len(cert.Certificate) == 0 {
		return false
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return false
	}
	for _, name := range leaf.DNSNames {
		if strings.EqualFold(name, host) {
			return true
		}
	}
	return false
}

func certificateFingerprint(cert tls.Certificate) string {
	if len(cert.Certificate) == 0 {
		return ""
	}
	digest := sha256.Sum256(cert.Certificate[0])
	return hex.EncodeToString(digest[:])
}
