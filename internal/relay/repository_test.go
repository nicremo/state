package relay

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteRepositoryEncryptsAPNSTokenAndPersistsRoute(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "relay.db")
	key := bytes.Repeat([]byte{0x42}, 32)
	repository, err := OpenSQLiteRepository(path, key)
	if err != nil {
		t.Fatalf("OpenSQLiteRepository() error = %v", err)
	}
	token := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	route := Route{
		ID:             "01989fdb-0566-7975-a306-fd8f8270ab06",
		APNSToken:      token,
		Environment:    EnvironmentSandbox,
		CapabilityHash: hashCapability("secret"),
		AttestationKey: "attestation-key",
		CreatedAt:      time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC),
	}
	if err := repository.CreateRoute(context.Background(), route); err != nil {
		t.Fatalf("CreateRoute() error = %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if bytes.Contains(contents, []byte(token)) {
		t.Fatal("relay database contains plaintext APNs token")
	}

	reopened, err := OpenSQLiteRepository(path, key)
	if err != nil {
		t.Fatalf("reopen repository error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	loaded, err := reopened.GetRoute(context.Background(), route.ID)
	if err != nil {
		t.Fatalf("GetRoute() error = %v", err)
	}
	if loaded.APNSToken != token || loaded.Environment != route.Environment || !bytes.Equal(loaded.CapabilityHash, route.CapabilityHash) {
		t.Fatalf("loaded route = %#v", loaded)
	}
	updatedToken := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := reopened.UpdateRouteToken(context.Background(), route.ID, updatedToken); err != nil {
		t.Fatalf("UpdateRouteToken() error = %v", err)
	}
	loaded, err = reopened.GetRoute(context.Background(), route.ID)
	if err != nil || loaded.APNSToken != updatedToken {
		t.Fatalf("updated route = %#v, %v", loaded, err)
	}
}

func TestSQLiteRepositoryConsumesChallengeOnce(t *testing.T) {
	t.Parallel()

	repository, err := OpenSQLiteRepository(filepath.Join(t.TempDir(), "relay.db"), bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatalf("OpenSQLiteRepository() error = %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	challenge := Challenge{Value: "single-use", ExpiresAt: now.Add(time.Minute)}
	if err := repository.CreateChallenge(context.Background(), challenge); err != nil {
		t.Fatalf("CreateChallenge() error = %v", err)
	}
	if err := repository.ConsumeChallenge(context.Background(), challenge.Value, now); err != nil {
		t.Fatalf("ConsumeChallenge() error = %v", err)
	}
	if err := repository.ConsumeChallenge(context.Background(), challenge.Value, now); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("second ConsumeChallenge() error = %v", err)
	}
	expired := Challenge{Value: "expired", ExpiresAt: now.Add(-time.Second)}
	if err := repository.CreateChallenge(context.Background(), expired); err != nil {
		t.Fatalf("CreateChallenge(expired) error = %v", err)
	}
	if err := repository.ConsumeChallenge(context.Background(), expired.Value, now); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("expired ConsumeChallenge() error = %v", err)
	}
}

func TestSQLiteRepositoryRejectsInvalidEncryptionKey(t *testing.T) {
	t.Parallel()

	if _, err := OpenSQLiteRepository(filepath.Join(t.TempDir(), "relay.db"), []byte("short")); err == nil {
		t.Fatal("OpenSQLiteRepository() accepted invalid key")
	}
}
