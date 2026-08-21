package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
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

func TestRunnerPairingListingAndRevocation(t *testing.T) {
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
		Kind:        state.ActorKindRunner,
		DisplayName: "CI Runner",
	})
	if err != nil {
		t.Fatalf("CreatePairingCode(runner) error = %v", err)
	}
	runnerCredential, err := manager.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode(runner) error = %v", err)
	}
	if runnerCredential.Actor.Kind != state.ActorKindRunner ||
		runnerCredential.Actor.Harness != "" ||
		runnerCredential.Actor.DeviceName != "" {
		t.Fatalf("runner actor = %#v", runnerCredential.Actor)
	}
	authenticatedRunner, err := manager.Authenticate(context.Background(), runnerCredential.Token)
	if err != nil {
		t.Fatalf("Authenticate(runner) error = %v", err)
	}
	if authenticatedRunner.Kind != state.ActorKindRunner || authenticatedRunner.ID != runnerCredential.Actor.ID {
		t.Fatalf("authenticated runner = %#v", authenticatedRunner)
	}

	runners, err := manager.ListActors(context.Background(), owner, state.ActorKindRunner)
	if err != nil {
		t.Fatalf("ListActors(runner) error = %v", err)
	}
	if len(runners) != 1 || runners[0].Actor.Kind != state.ActorKindRunner || runners[0].Actor.DisplayName != "CI Runner" {
		t.Fatalf("runners = %#v", runners)
	}
	devices, err := manager.ListActors(context.Background(), owner, state.ActorKindDevice)
	if err != nil {
		t.Fatalf("ListActors(device) error = %v", err)
	}
	if len(devices) != 1 || devices[0].Actor.Kind != state.ActorKindOwner {
		t.Fatalf("devices = %#v, want owner only", devices)
	}
	if _, err := manager.ListActors(context.Background(), runnerCredential.Actor, state.ActorKindRunner); !errors.Is(err, state.ErrForbidden) {
		t.Fatalf("runner ListActors() error = %v, want ErrForbidden", err)
	}

	if err := manager.RevokeActor(context.Background(), owner, runnerCredential.Actor.ID); err != nil {
		t.Fatalf("RevokeActor(runner) error = %v", err)
	}
	if _, err := manager.Authenticate(context.Background(), runnerCredential.Token); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("revoked runner Authenticate() error = %v, want ErrInvalidCredential", err)
	}
}

func TestRunnerPairingCodeValidation(t *testing.T) {
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

	rejected := []PairingCodeRequest{
		{Kind: state.ActorKindRunner, DisplayName: "CI Runner", Harness: "codex"},       // runners have no harness
		{Kind: state.ActorKindRunner, DisplayName: "CI Runner", DeviceName: "Mac mini"}, // runners have no device name
		{Kind: state.ActorKindRunner},                        // display name required
		{Kind: state.ActorKindSystem, DisplayName: "System"}, // system cannot be paired
		{Kind: "bogus", DisplayName: "Bogus"},
	}
	for _, request := range rejected {
		if _, err := manager.CreatePairingCode(context.Background(), owner, request); !errors.Is(err, state.ErrInvalidInput) {
			t.Fatalf("CreatePairingCode(%#v) error = %v, want ErrInvalidInput", request, err)
		}
	}
	if _, err := manager.ListActors(context.Background(), owner, state.ActorKindOwner); !errors.Is(err, state.ErrInvalidInput) {
		t.Fatalf("ListActors(owner) error = %v, want ErrInvalidInput", err)
	}
	if _, err := manager.ListActors(context.Background(), owner, state.ActorKind("bogus")); !errors.Is(err, state.ErrInvalidInput) {
		t.Fatalf("ListActors(bogus) error = %v, want ErrInvalidInput", err)
	}
}

func TestActorKindRunnerMigrationFromLegacySchema(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
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

	// The schema as deployed before the runner kind existed.
	legacyStatements := []string{
		`CREATE TABLE state_actors (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK(kind IN ('owner', 'device', 'harness', 'system')),
			display_name TEXT NOT NULL,
			harness TEXT NOT NULL DEFAULT '',
			device_name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			revoked_at TEXT
		) STRICT`,
		`CREATE UNIQUE INDEX state_single_owner_idx
			ON state_actors(kind) WHERE kind = 'owner'`,
		`CREATE TABLE state_credentials (
			id TEXT PRIMARY KEY,
			actor_id TEXT NOT NULL REFERENCES state_actors(id),
			token_hash TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL,
			last_used_at TEXT,
			revoked_at TEXT
		) STRICT`,
		`CREATE TABLE state_pairing_codes (
			id TEXT PRIMARY KEY,
			code_hash TEXT NOT NULL UNIQUE,
			actor_kind TEXT NOT NULL DEFAULT 'harness' CHECK(actor_kind IN ('device', 'harness')),
			harness TEXT NOT NULL,
			display_name TEXT NOT NULL,
			device_name TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL REFERENCES state_actors(id),
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			used_at TEXT
		) STRICT`,
		// An owner with a working credential and an unused pairing code, so the
		// rebuild must prove it preserves rows and cross-table references.
		fmt.Sprintf(`INSERT INTO state_actors (id, kind, display_name, harness, device_name, created_at)
			VALUES ('owner-1', 'owner', 'Fabian', '', 'iPhone', '%s')`, formatTime(now)),
		fmt.Sprintf(`INSERT INTO state_credentials (id, actor_id, token_hash, created_at)
			VALUES ('credential-1', 'owner-1', '%s', '%s')`, hashSecret("owner-token"), formatTime(now)),
		fmt.Sprintf(`INSERT INTO state_pairing_codes (
			id, code_hash, actor_kind, harness, display_name, device_name, created_by, created_at, expires_at
		) VALUES ('pairing-1', '%s', 'device', '', 'Fabian', 'iPad', 'owner-1', '%s', '%s')`,
			hashSecret(normalizePairingCode("LEGACY-CODE1")), formatTime(now), formatTime(now.Add(10*time.Minute))),
	}
	for _, statement := range legacyStatements {
		if _, err := app.DB().NewQuery(statement).Execute(); err != nil {
			t.Fatalf("create legacy schema error = %v", err)
		}
	}

	manager, err := NewManager(app, "bootstrap-secret", WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewManager() over legacy schema error = %v", err)
	}

	owner, err := manager.Authenticate(context.Background(), "owner-token")
	if err != nil {
		t.Fatalf("Authenticate(owner) after migration error = %v", err)
	}
	if owner.ID != "owner-1" || owner.Kind != state.ActorKindOwner {
		t.Fatalf("owner after migration = %#v", owner)
	}
	indexRow := struct {
		Name string `db:"name"`
	}{}
	if err := app.DB().NewQuery(`
		SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'state_single_owner_idx'
	`).One(&indexRow); err != nil {
		t.Fatalf("state_single_owner_idx missing after migration: %v", err)
	}
	if _, err := manager.BootstrapOwner(context.Background(), "bootstrap-secret", OwnerBootstrapRequest{DisplayName: "Other"}); !errors.Is(err, ErrOwnerExists) {
		t.Fatalf("second BootstrapOwner() error = %v, want ErrOwnerExists", err)
	}

	deviceCredential, err := manager.ExchangePairingCode(context.Background(), "LEGACY-CODE1")
	if err != nil {
		t.Fatalf("ExchangePairingCode(legacy code) error = %v", err)
	}
	if deviceCredential.Actor.Kind != state.ActorKindDevice || deviceCredential.Actor.DeviceName != "iPad" {
		t.Fatalf("device actor from legacy code = %#v", deviceCredential.Actor)
	}

	pairing, err := manager.CreatePairingCode(context.Background(), owner, PairingCodeRequest{
		Kind:        state.ActorKindRunner,
		DisplayName: "CI Runner",
	})
	if err != nil {
		t.Fatalf("CreatePairingCode(runner) after migration error = %v", err)
	}
	runnerCredential, err := manager.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode(runner) after migration error = %v", err)
	}
	if runnerCredential.Actor.Kind != state.ActorKindRunner {
		t.Fatalf("runner actor after migration = %#v", runnerCredential.Actor)
	}

	// Re-running the schema on the migrated database is a no-op.
	if err := manager.ensureSchema(); err != nil {
		t.Fatalf("second ensureSchema() error = %v", err)
	}
	if _, err := manager.Authenticate(context.Background(), "owner-token"); err != nil {
		t.Fatalf("Authenticate(owner) after second ensureSchema error = %v", err)
	}
	if _, err := manager.Authenticate(context.Background(), runnerCredential.Token); err != nil {
		t.Fatalf("Authenticate(runner) after second ensureSchema error = %v", err)
	}
}

func TestFreshSchemaIncludesRunnerKind(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	manager := newTestManager(t, &now)
	for _, table := range []string{"state_actors", "state_pairing_codes"} {
		row := struct {
			SQL string `db:"sql"`
		}{}
		if err := manager.app.DB().NewQuery(`
			SELECT sql FROM sqlite_master WHERE type = 'table' AND name = {:table}
		`).Bind(dbx.Params{"table": table}).One(&row); err != nil {
			t.Fatalf("read %s schema error = %v", table, err)
		}
		if !strings.Contains(row.SQL, "'runner'") {
			t.Fatalf("fresh %s schema does not admit runner:\n%s", table, row.SQL)
		}
	}
	leftovers := []struct {
		Name string `db:"name"`
	}{}
	if err := manager.app.DB().NewQuery(`
		SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE '%\_old' ESCAPE '\'
	`).All(&leftovers); err != nil {
		t.Fatalf("check leftover tables error = %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("unexpected leftover tables: %#v", leftovers)
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
