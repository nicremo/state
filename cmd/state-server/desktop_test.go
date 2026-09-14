package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
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
	"strings"
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

func TestDesktopRejectsPublicNetworkAndInvalidHost(t *testing.T) {
	handler := desktopLANOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for address, expected := range map[string]int{"192.168.1.2:5000": 204, "127.0.0.1:5000": 204, "[::1]:5000": 204, "8.8.8.8:5000": 403, "garbage": 403} {
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
