package securefile

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateAuditSigningKeyIsStableAndPrivate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keys", "audit-signing.key")
	first, err := LoadOrCreateAuditSigningKey(path)
	if err != nil {
		t.Fatalf("first LoadOrCreateAuditSigningKey() error = %v", err)
	}
	second, err := LoadOrCreateAuditSigningKey(path)
	if err != nil {
		t.Fatalf("second LoadOrCreateAuditSigningKey() error = %v", err)
	}
	if len(first) != ed25519.PrivateKeySize || !bytes.Equal(first, second) {
		t.Fatal("audit signing key is invalid or changed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadOrCreateBootstrapTokenIsStableAndPrivate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "instance", "bootstrap.token")
	first, err := LoadOrCreateBootstrapToken(path)
	if err != nil {
		t.Fatalf("first LoadOrCreateBootstrapToken() error = %v", err)
	}
	second, err := LoadOrCreateBootstrapToken(path)
	if err != nil {
		t.Fatalf("second LoadOrCreateBootstrapToken() error = %v", err)
	}
	if first == "" || first != second {
		t.Fatal("bootstrap token is empty or changed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadOrCreateRejectsInsecureExistingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bootstrap.token")
	if err := os.WriteFile(path, []byte("unsafe"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := LoadOrCreateBootstrapToken(path)
	if err == nil {
		t.Fatal("LoadOrCreateBootstrapToken() succeeded for insecure permissions")
	}
}
