package store

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
)

func TestPocketBaseRepositoryPersistsIdempotentMutationAcrossRestart(t *testing.T) {
	t.Parallel()

	dataDirectory := t.TempDir()
	privateKey := deterministicSigningKey()
	app := bootstrappedApp(t, dataDirectory)
	repository, err := NewPocketBaseRepository(app, privateKey)
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	service := state.NewService(repository)
	actor := state.Actor{
		ID:          "01989d9b-b5c4-7f9e-88f2-4347a1e90f16",
		Kind:        state.ActorKindHarness,
		DisplayName: "Codex",
		Harness:     "codex",
		DeviceName:  "MacBook",
	}
	input := state.CreateReminderInput{
		Title:           "Review the deployment",
		ClientRequestID: "01989d9b-b5c4-7ddd-94ff-b9920730349d",
		SourceExcerpt:   "Remind me to review the deployment",
	}

	created, err := service.CreateReminder(context.Background(), actor, input)
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	if err := app.ResetBootstrapState(); err != nil {
		t.Fatalf("ResetBootstrapState() error = %v", err)
	}

	restartedApp := bootstrappedApp(t, dataDirectory)
	restartedRepository, err := NewPocketBaseRepository(restartedApp, privateKey)
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() after restart error = %v", err)
	}
	restartedService := state.NewService(restartedRepository)
	replayed, err := restartedService.CreateReminder(context.Background(), actor, input)
	if err != nil {
		t.Fatalf("replayed CreateReminder() error = %v", err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("replayed ID = %q, want %q", replayed.ID, created.ID)
	}
	events, err := restartedRepository.ListAuditEvents(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(events))
	}
	if err := restartedRepository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestPocketBaseRepositoryMakesAuditEventsImmutable(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	repository, err := NewPocketBaseRepository(app, deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	service := state.NewService(repository)
	created, err := service.CreateReminder(context.Background(), state.Actor{
		ID:   "01989d9b-b5c4-7187-8ea7-68f05ecdc3d9",
		Kind: state.ActorKindOwner,
	}, state.CreateReminderInput{
		Title:           "Immutable history",
		ClientRequestID: "01989d9b-b5c4-7753-a0c6-4cf11f273f6d",
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	events, err := repository.ListAuditEvents(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(events))
	}

	_, updateErr := app.DB().NewQuery("UPDATE state_audit_events SET action = 'tampered' WHERE id = {:id}").
		Bind(dbx.Params{"id": events[0].ID}).
		Execute()
	if updateErr == nil || !strings.Contains(updateErr.Error(), "immutable") {
		t.Fatalf("audit update error = %v, want immutable trigger error", updateErr)
	}
	_, deleteErr := app.DB().NewQuery("DELETE FROM state_audit_events WHERE id = {:id}").
		Bind(dbx.Params{"id": events[0].ID}).
		Execute()
	if deleteErr == nil || !strings.Contains(deleteErr.Error(), "immutable") {
		t.Fatalf("audit delete error = %v, want immutable trigger error", deleteErr)
	}
}

func TestPocketBaseRepositoryRejectsConcurrentRevision(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	repository, err := NewPocketBaseRepository(app, deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	service := state.NewService(repository)
	actor := state.Actor{ID: "01989d9b-b5c4-7d84-a5ec-dc3821c97155", Kind: state.ActorKindDevice}
	created, err := service.CreateReminder(context.Background(), actor, state.CreateReminderInput{
		Title:           "Concurrent update",
		ClientRequestID: "01989d9b-b5c4-7b94-b62b-e0d5866368c0",
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}

	firstTitle := "First update"
	_, err = service.UpdateReminder(context.Background(), actor, created.ID, state.UpdateReminderInput{
		Title:            &firstTitle,
		ExpectedRevision: 1,
		ClientRequestID:  "01989d9b-b5c4-7484-a36d-351efcfe1f16",
	})
	if err != nil {
		t.Fatalf("first UpdateReminder() error = %v", err)
	}
	staleTitle := "Stale update"
	_, err = service.UpdateReminder(context.Background(), actor, created.ID, state.UpdateReminderInput{
		Title:            &staleTitle,
		ExpectedRevision: 1,
		ClientRequestID:  "01989d9b-b5c4-7104-b62d-b9ec26b940d4",
	})
	if !errors.Is(err, state.ErrRevisionConflict) {
		t.Fatalf("stale UpdateReminder() error = %v, want ErrRevisionConflict", err)
	}
}

func bootstrappedApp(t *testing.T, dataDirectory string) *pocketbase.PocketBase {
	t.Helper()

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:   dataDirectory,
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
		resetErr := app.ResetBootstrapState()
		if resetErr != nil && !errors.Is(resetErr, sql.ErrConnDone) {
			t.Errorf("ResetBootstrapState() error = %v", resetErr)
		}
	})
	return app
}

func deterministicSigningKey() ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	return ed25519.NewKeyFromSeed(seed)
}
