package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/pocketbase"
)

func TestOwnerBootstrapAndHarnessPairing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	manager := newTestManager(t, &now)
	ownerCredential, err := manager.BootstrapOwner(context.Background(), "bootstrap-secret", OwnerBootstrapRequest{
		DisplayName: "Fabian",
		DeviceName:  "iPhone",
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() error = %v", err)
	}
	if ownerCredential.Token == "" || ownerCredential.Actor.Kind != state.ActorKindOwner {
		t.Fatalf("unexpected owner credential: %#v", ownerCredential)
	}
	owner, err := manager.Authenticate(context.Background(), ownerCredential.Token)
	if err != nil {
		t.Fatalf("Authenticate(owner) error = %v", err)
	}

	pairing, err := manager.CreatePairingCode(context.Background(), owner, PairingCodeRequest{
		Harness:     "claude-code",
		DisplayName: "Claude Code",
		DeviceName:  "MacBook",
	})
	if err != nil {
		t.Fatalf("CreatePairingCode() error = %v", err)
	}
	if pairing.Code == "" || !pairing.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("unexpected pairing code: %#v", pairing)
	}
	harnessCredential, err := manager.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode() error = %v", err)
	}
	if harnessCredential.Actor.Kind != state.ActorKindHarness || harnessCredential.Actor.Harness != "claude-code" {
		t.Fatalf("unexpected harness actor: %#v", harnessCredential.Actor)
	}
	authenticatedHarness, err := manager.Authenticate(context.Background(), harnessCredential.Token)
	if err != nil {
		t.Fatalf("Authenticate(harness) error = %v", err)
	}
	if authenticatedHarness.ID != harnessCredential.Actor.ID {
		t.Fatalf("authenticated actor ID = %q, want %q", authenticatedHarness.ID, harnessCredential.Actor.ID)
	}

	_, err = manager.ExchangePairingCode(context.Background(), pairing.Code)
	if !errors.Is(err, ErrPairingCodeUsed) {
		t.Fatalf("replayed ExchangePairingCode() error = %v, want ErrPairingCodeUsed", err)
	}
}

func TestPairingCodeExpiresAndCredentialCanBeRevoked(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	manager := newTestManager(t, &now)
	ownerCredential, err := manager.BootstrapOwner(context.Background(), "bootstrap-secret", OwnerBootstrapRequest{
		DisplayName: "Fabian",
		DeviceName:  "iPhone",
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() error = %v", err)
	}
	owner, err := manager.Authenticate(context.Background(), ownerCredential.Token)
	if err != nil {
		t.Fatalf("Authenticate(owner) error = %v", err)
	}
	expired, err := manager.CreatePairingCode(context.Background(), owner, PairingCodeRequest{
		Harness:     "codex",
		DisplayName: "Codex",
		DeviceName:  "MacBook",
	})
	if err != nil {
		t.Fatalf("CreatePairingCode() error = %v", err)
	}
	now = now.Add(11 * time.Minute)
	_, err = manager.ExchangePairingCode(context.Background(), expired.Code)
	if !errors.Is(err, ErrPairingCodeExpired) {
		t.Fatalf("expired ExchangePairingCode() error = %v, want ErrPairingCodeExpired", err)
	}

	valid, err := manager.CreatePairingCode(context.Background(), owner, PairingCodeRequest{
		Harness:     "opencode",
		DisplayName: "OpenCode",
		DeviceName:  "MacBook",
	})
	if err != nil {
		t.Fatalf("second CreatePairingCode() error = %v", err)
	}
	credential, err := manager.ExchangePairingCode(context.Background(), valid.Code)
	if err != nil {
		t.Fatalf("second ExchangePairingCode() error = %v", err)
	}
	if err := manager.RevokeActor(context.Background(), owner, credential.Actor.ID); err != nil {
		t.Fatalf("RevokeActor() error = %v", err)
	}
	_, err = manager.Authenticate(context.Background(), credential.Token)
	if !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("revoked Authenticate() error = %v, want ErrInvalidCredential", err)
	}
}

func TestOwnerBootstrapIsOneTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	manager := newTestManager(t, &now)
	_, err := manager.BootstrapOwner(context.Background(), "wrong", OwnerBootstrapRequest{DisplayName: "Fabian"})
	if !errors.Is(err, ErrInvalidBootstrapToken) {
		t.Fatalf("wrong BootstrapOwner() error = %v, want ErrInvalidBootstrapToken", err)
	}
	_, err = manager.BootstrapOwner(context.Background(), "bootstrap-secret", OwnerBootstrapRequest{DisplayName: "Fabian"})
	if err != nil {
		t.Fatalf("BootstrapOwner() error = %v", err)
	}
	_, err = manager.BootstrapOwner(context.Background(), "bootstrap-secret", OwnerBootstrapRequest{DisplayName: "Other"})
	if !errors.Is(err, ErrOwnerExists) {
		t.Fatalf("second BootstrapOwner() error = %v, want ErrOwnerExists", err)
	}
}

func TestDevicePairingAndActorListing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	manager := newTestManager(t, &now)
	ownerCredential, err := manager.BootstrapOwner(context.Background(), "bootstrap-secret", OwnerBootstrapRequest{
		DisplayName: "Fabian",
		DeviceName:  "iPhone",
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() error = %v", err)
	}
	owner, err := manager.Authenticate(context.Background(), ownerCredential.Token)
	if err != nil {
		t.Fatalf("Authenticate(owner) error = %v", err)
	}
	pairing, err := manager.CreatePairingCode(context.Background(), owner, PairingCodeRequest{
		Kind:        state.ActorKindDevice,
		DisplayName: "Fabian",
		DeviceName:  "iPad",
	})
	if err != nil {
		t.Fatalf("CreatePairingCode(device) error = %v", err)
	}
	deviceCredential, err := manager.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode(device) error = %v", err)
	}
	if deviceCredential.Actor.Kind != state.ActorKindDevice || deviceCredential.Actor.DeviceName != "iPad" {
		t.Fatalf("device actor = %#v", deviceCredential.Actor)
	}

	agents, err := manager.ListActors(context.Background(), owner, state.ActorKindHarness)
	if err != nil {
		t.Fatalf("ListActors(harness) error = %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("agents = %#v, want none", agents)
	}
	devices, err := manager.ListActors(context.Background(), owner, state.ActorKindDevice)
	if err != nil {
		t.Fatalf("ListActors(device) error = %v", err)
	}
	if len(devices) != 2 || devices[0].Actor.Kind != state.ActorKindOwner || devices[1].Actor.Kind != state.ActorKindDevice {
		t.Fatalf("devices = %#v", devices)
	}
	if _, err := manager.ListActors(context.Background(), deviceCredential.Actor, state.ActorKindHarness); !errors.Is(err, state.ErrForbidden) {
		t.Fatalf("device ListActors() error = %v, want ErrForbidden", err)
	}
}

func TestCredentialRotationAndSelfRevocation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	manager := newTestManager(t, &now)
	ownerCredential, err := manager.BootstrapOwner(context.Background(), "bootstrap-secret", OwnerBootstrapRequest{
		DisplayName: "Fabian",
		DeviceName:  "iPhone",
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() error = %v", err)
	}
	rotated, err := manager.RotateCredential(context.Background(), ownerCredential.Token)
	if err != nil {
		t.Fatalf("RotateCredential() error = %v", err)
	}
	if rotated.Token == "" || rotated.Token == ownerCredential.Token || rotated.Actor.ID != ownerCredential.Actor.ID {
		t.Fatalf("rotated credential = %#v", rotated)
	}
	if _, err := manager.Authenticate(context.Background(), ownerCredential.Token); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("old Authenticate() error = %v, want ErrInvalidCredential", err)
	}
	if _, err := manager.Authenticate(context.Background(), rotated.Token); err != nil {
		t.Fatalf("new Authenticate() error = %v", err)
	}
	if err := manager.RevokeCredential(context.Background(), rotated.Token); err != nil {
		t.Fatalf("RevokeCredential() error = %v", err)
	}
	if _, err := manager.Authenticate(context.Background(), rotated.Token); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("revoked Authenticate() error = %v, want ErrInvalidCredential", err)
	}
}

func newTestManager(t *testing.T, now *time.Time) *Manager {
	t.Helper()

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:   t.TempDir(),
		HideStartBanner:  true,
		DataMaxOpenConns: 1,
		DataMaxIdleConns: 1,
		AuxMaxOpenConns:  1,
		AuxMaxIdleConns:  1,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	t.Cleanup(func() {
		if err := app.ResetBootstrapState(); err != nil {
			t.Errorf("ResetBootstrapState() error = %v", err)
		}
	})
	manager, err := NewManager(app, "bootstrap-secret", WithClock(func() time.Time { return *now }))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}
