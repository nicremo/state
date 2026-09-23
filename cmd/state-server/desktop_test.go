package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDesktopCertificatePersistsAndRejectsUnsafeFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.pem")
	first, fingerprint, err := desktopCertificate(path, "test.local")
	if err != nil {
		t.Fatal(err)
	}
	if len(fingerprint) != 64 || len(first.Certificate) != 1 {
		t.Fatal("invalid identity")
	}
	_, again, err := desktopCertificate(path, "test.local")
	if err != nil || again != fingerprint {
		t.Fatal("identity changed on restart")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := desktopCertificate(path, "test.local"); err == nil {
		t.Fatal("accepted exposed private identity")
	}
}

func TestDesktopCertificateFollowsHostnameChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.pem")
	first, firstFingerprint, err := desktopCertificate(path, "alt.local")
	if err != nil {
		t.Fatal(err)
	}
	if names := certificateDNSNames(t, first); !slices.Contains(names, "alt.local") {
		t.Fatalf("first identity has wrong names: %v", names)
	}
	second, secondFingerprint, err := desktopCertificate(path, "neu.local")
	if err != nil {
		t.Fatal(err)
	}
	if names := certificateDNSNames(t, second); !slices.Contains(names, "neu.local") {
		t.Fatalf("stored identity kept the old hostname: %v", names)
	}
	if secondFingerprint == firstFingerprint {
		t.Fatal("hostname change did not replace the identity")
	}
	_, again, err := desktopCertificate(path, "neu.local")
	if err != nil || again != secondFingerprint {
		t.Fatal("replacement identity was not persisted")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("replacement identity is not private")
	}
}

func certificateDNSNames(t *testing.T, cert tls.Certificate) []string {
	t.Helper()
	if len(cert.Certificate) == 0 {
		t.Fatal("identity has no certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.DNSNames
}

func TestDesktopRejectsPublicNetworkAndInvalidHost(t *testing.T) {
	handler := desktopLANOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for address, expected := range map[string]int{
		"192.168.1.2:5000":                204,
		"127.0.0.1:5000":                  204,
		"[::1]:5000":                      204,
		"[fe80::1]:5000":                  204,
		"[fe80::1%en0]:5000":              204,
		"[fd00::1]:5000":                  204,
		"[::ffff:192.168.1.2]:5000":       204,
		"8.8.8.8:5000":                    403,
		"[::ffff:8.8.8.8]:5000":           403,
		"[2606:4700:4700::1111]:5000":     403,
		"[2606:4700:4700::1111%en0]:5000": 403,
		"garbage":                         403,
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = address
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatalf("%s got %d", address, w.Code)
		}
	}
	for _, host := range []string{"", "example.com", "name.local/evil", "bad name.local", "-bad.local"} {
		if validDesktopHost(host) {
			t.Fatalf("accepted %q", host)
		}
	}
	if !validDesktopHost("Fabians-Mac.local") {
		t.Fatal("valid Bonjour name rejected")
	}
}

func TestValidDesktopRelayURL(t *testing.T) {
	for value, expected := range map[string]bool{
		"":                                  true,
		"https://relay.example.com":         true,
		"https://relay.example.com:8443":    true,
		"https://relay.example.com/push":    true,
		"http://relay.example.com":          false,
		"https://user@relay.example.com":    false,
		"https://user:pw@relay.example.com": false,
		"https://relay.example.com/?x=1":    false,
		"https://relay.example.com/#frag":   false,
		"relay.example.com":                 false,
		"//relay.example.com":               false,
		"https://":                          false,
		"not a url":                         false,
	} {
		if validDesktopRelayURL(value) != expected {
			t.Fatalf("validDesktopRelayURL(%q) = %v", value, !expected)
		}
	}
}

func TestDesktopPairingURLCarriesTheRelay(t *testing.T) {
	parsed, err := url.Parse(desktopPairingURL("https://mac.local:9847", "code-1", "fingerprint-1", "https://relay.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "state" || parsed.Host != "pair" {
		t.Fatalf("unexpected pairing scheme: %s", parsed)
	}
	query := parsed.Query()
	if query.Get("server") != "https://mac.local:9847" || query.Get("code") != "code-1" ||
		query.Get("fingerprint") != "fingerprint-1" || query.Get("relay") != "https://relay.example.com" {
		t.Fatalf("pairing link lost values: %v", query)
	}
	without, err := url.Parse(desktopPairingURL("https://mac.local:9847", "code-1", "fingerprint-1", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, present := without.Query()["relay"]; present {
		t.Fatal("pairing link advertises a relay without configuration")
	}
}

func TestDesktopRejectsInvalidRelayURL(t *testing.T) {
	input, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	err := runDesktop(
		[]string{"--data", t.TempDir(), "--host", "test.local", "--https", "127.0.0.1:0", "--local-http", "127.0.0.1:0", "--relay-url", "http://relay.example.com"},
		input, io.Discard, io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err == nil {
		t.Fatal("desktop started with a plain HTTP relay")
	}
}

func TestDesktopAdvertisesConfiguredRelay(t *testing.T) {
	data := t.TempDir()
	input, writer := io.Pipe()
	output, result := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := runDesktop(
			[]string{"--data", data, "--host", "test.local", "--https", "127.0.0.1:0", "--local-http", "127.0.0.1:0", "--relay-url", "https://relay.example.com"},
			input, result, io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil)),
		)
		_ = result.Close()
		done <- err
	}()
	reader := json.NewDecoder(output)
	var status desktopStatus
	if err := reader.Decode(&status); err != nil {
		t.Fatal("read desktop status:", err)
	}
	if status.RelayURL != "https://relay.example.com" {
		t.Fatalf("status does not advertise the relay: %q", status.RelayURL)
	}
	if _, err := io.WriteString(writer, "{\"action\":\"pair\"}\n"); err != nil {
		t.Fatal(err)
	}
	if err := reader.Decode(&status); err != nil {
		t.Fatal("read desktop status:", err)
	}
	if status.Pairing == nil {
		t.Fatal("missing pairing")
	}
	pairing, err := url.Parse(status.Pairing.URL)
	if err != nil {
		t.Fatal(err)
	}
	if pairing.Query().Get("relay") != "https://relay.example.com" {
		t.Fatalf("pairing link does not carry the relay: %s", status.Pairing.URL)
	}
	_ = writer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("desktop server did not stop")
	}
}

// syncWriter collects the desktop server's stderr while it runs on another goroutine.
type syncWriter struct {
	mutex sync.Mutex
	text  bytes.Buffer
}

func (w *syncWriter) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.text.Write(data)
}

func (w *syncWriter) String() string {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.text.String()
}

func TestDesktopLogsAddressAndVersion(t *testing.T) {
	data := t.TempDir()
	input, writer := io.Pipe()
	output, result := io.Pipe()
	stderr := &syncWriter{}
	done := make(chan error, 1)
	go func() {
		err := runDesktop(
			[]string{"--data", data, "--host", "test.local", "--https", "127.0.0.1:0", "--local-http", "127.0.0.1:0"},
			input, result, stderr, slog.New(slog.NewTextHandler(stderr, nil)),
		)
		_ = result.Close()
		done <- err
	}()
	var status desktopStatus
	if err := json.NewDecoder(output).Decode(&status); err != nil {
		t.Fatal("read desktop status:", err)
	}
	_ = writer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("desktop server did not stop")
	}
	logs := stderr.String()
	if !strings.Contains(logs, "state-server desktop listening") ||
		!strings.Contains(logs, status.ServerURL) || !strings.Contains(logs, "version="+version) {
		t.Fatalf("stderr log lacks address or version: %q", logs)
	}
}

func TestDesktopPairingTLSAndRestart(t *testing.T) {
	data := t.TempDir()
	type running struct {
		in     *io.PipeWriter
		reader *json.Decoder
		done   chan error
		output *io.PipeReader
	}
	start := func() running {
		input, writer := io.Pipe()
		output, result := io.Pipe()
		done := make(chan error, 1)
		go func() {
			err := runDesktop([]string{"--data", data, "--host", "test.local", "--https", "127.0.0.1:0", "--local-http", "127.0.0.1:0"}, input, result, io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil)))
			_ = result.Close()
			done <- err
		}()
		r := running{writer, json.NewDecoder(output), done, output}
		t.Cleanup(func() { _ = writer.Close(); _ = output.Close() })
		return r
	}
	read := func(r running) desktopStatus {
		t.Helper()
		var status desktopStatus
		if err := r.reader.Decode(&status); err != nil {
			t.Fatal("read desktop status:", err)
		}
		return status
	}
	stop := func(r running) {
		t.Helper()
		_ = r.in.Close()
		select {
		case err := <-r.done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("server did not stop when parent closed pipe")
		}
	}
	r := start()
	initial := read(r)
	if len(initial.Devices) != 0 {
		t.Fatal("new server has paired devices")
	}
	publicURL, _ := url.Parse(initial.ServerURL)
	publicURL.Host = net.JoinHostPort("127.0.0.1", publicURL.Port())
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, // Test the same explicit certificate pin as iOS.
		VerifyConnection: func(connection tls.ConnectionState) error {
			digest := sha256.Sum256(connection.PeerCertificates[0].Raw)
			if hex.EncodeToString(digest[:]) != initial.Fingerprint {
				return fmt.Errorf("certificate mismatch")
			}
			return nil
		},
	}}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	get, err := client.Get(publicURL.String() + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	_ = get.Body.Close()
	if get.StatusCode != 200 {
		t.Fatal("TLS health failed")
	}
	for _, path := range []string{"/api/v1/devices", "/mcp"} {
		request, _ := http.NewRequest("GET", publicURL.String()+path, nil)
		if path == "/mcp" {
			request.Method = "POST"
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatalf("unauthenticated %s accepted: %d", path, response.StatusCode)
		}
	}
	_, _ = io.WriteString(r.in, "{\"action\":\"pair\"}\n")
	paired := read(r)
	if paired.Pairing == nil {
		t.Fatal("missing pairing")
	}
	payload, _ := url.Parse(paired.Pairing.URL)
	if payload.Query().Get("fingerprint") != initial.Fingerprint || payload.Query().Get("server") != initial.ServerURL {
		t.Fatal("QR identity does not match server")
	}
	_, _ = io.WriteString(r.in, "{\"action\":\"pair\"}\n")
	if again := read(r); again.Pairing.Code != paired.Pairing.Code {
		t.Fatal("unexpired code was not reused")
	}
	body := fmt.Sprintf(`{"code":%q}`, paired.Pairing.Code)
	exchange, err := client.Post(publicURL.String()+"/api/v1/pairing/exchange", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if exchange.StatusCode != 201 {
		t.Fatal("pairing exchange failed:", exchange.StatusCode)
	}
	var credential struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(exchange.Body).Decode(&credential); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()
	replay, err := client.Post(publicURL.String()+"/api/v1/pairing/exchange", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = replay.Body.Close()
	if replay.StatusCode != 409 {
		t.Fatal("one-time pairing code was reused")
	}
	_, _ = io.WriteString(r.in, "{\"action\":\"status\"}\n")
	updated := read(r)
	if len(updated.Devices) != 1 || updated.Pairing != nil {
		t.Fatal("consumed device pairing was not cleared")
	}
	stop(r)
	r = start()
	restarted := read(r)
	if restarted.Fingerprint != initial.Fingerprint || len(restarted.Devices) != 1 {
		t.Fatal("identity or device lost on restart")
	}
	request, _ := http.NewRequestWithContext(context.Background(), "GET", restarted.LocalURL+"/api/v1/changes?after=0&limit=10", nil)
	request.Header.Set("Authorization", "Bearer "+credential.Token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("paired device cannot synchronize after restart:", response.StatusCode)
	}
	stop(r)
}

// A Mac Server without a paired iPhone still has to pair its own runner, so
// the private pipe mints runner codes as well as device and harness codes.
func TestDesktopCreatesRunnerPairingCodes(t *testing.T) {
	input, writer := io.Pipe()
	output, result := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := runDesktop([]string{"--data", t.TempDir(), "--host", "test.local", "--https", "127.0.0.1:0", "--local-http", "127.0.0.1:0"}, input, result, io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil)))
		_ = result.Close()
		done <- err
	}()
	t.Cleanup(func() { _ = writer.Close(); _ = output.Close(); <-done })
	reader := json.NewDecoder(output)
	var status desktopStatus
	if err := reader.Decode(&status); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(`{"action":"pair","kind":"runner","name":"Mac Runner"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := reader.Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Pairing == nil || status.Pairing.Kind != "runner" || status.Pairing.Code == "" {
		t.Fatalf("runner pairing missing: %+v", status.Pairing)
	}
	body := strings.NewReader(`{"code":"` + status.Pairing.Code + `"}`)
	response, err := http.Post(status.LocalURL+"/api/v1/pairing/exchange", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var credential struct {
		Actor struct {
			Kind        string `json:"kind"`
			DisplayName string `json:"display_name"`
		} `json:"actor"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&credential); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || credential.Actor.Kind != "runner" || credential.Actor.DisplayName != "Mac Runner" || credential.Token == "" {
		t.Fatalf("exchange = %d %+v", response.StatusCode, credential.Actor)
	}
}
