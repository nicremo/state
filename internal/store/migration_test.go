package store

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
)

// The legacy audit table forced a reminder on every event.
const legacyAuditEventsDDL = `CREATE TABLE state_audit_events (
	sequence INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	reminder_id TEXT NOT NULL REFERENCES state_reminders(id),
	action TEXT NOT NULL,
	revision INTEGER NOT NULL,
	client_request_id TEXT NOT NULL UNIQUE,
	previous_hash TEXT NOT NULL,
	hash TEXT NOT NULL UNIQUE,
	signature TEXT NOT NULL,
	server_time TEXT NOT NULL,
	event_json TEXT NOT NULL CHECK(json_valid(event_json))
) STRICT`

func auditSchemaDDL(t *testing.T, app interface {
	DB() dbx.Builder
}) string {
	t.Helper()
	row := struct {
		SQL string `db:"sql"`
	}{}
	if err := app.DB().NewQuery(`
		SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'state_audit_events'
	`).One(&row); err != nil {
		t.Fatalf("read audit schema DDL error = %v", err)
	}
	return row.SQL
}

func TestFreshDatabaseHasNullableAuditReminder(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	repository, err := NewPocketBaseRepository(app, deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	ddl := auditSchemaDDL(t, app)
	if strings.Contains(ddl, "reminder_id TEXT NOT NULL") {
		t.Fatalf("fresh audit schema keeps reminder_id NOT NULL: %s", ddl)
	}
	assertAuditTriggers(t, app)

	// A policy event without a reminder inserts cleanly.
	service := state.NewService(repository)
	project := mustStoreProject(t, service, "customer-api")
	if _, err := service.CreatePolicy(context.Background(), executorOwner, state.CreatePolicyInput{
		Name:                "nightly-review",
		ProjectID:           project.ID,
		Adapter:             "codex",
		Mode:                state.ExecutionModeSupervised,
		AllowedCapabilities: []string{state.CapabilityReadRepository},
		TimeoutMinutes:      30,
		ClientRequestID:     execRequestID(),
	}); err != nil {
		t.Fatalf("CreatePolicy() error = %v", err)
	}
	if err := repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
}

func TestMigrateAuditReminderNullableFromLegacySchema(t *testing.T) {
	t.Parallel()

	dataDirectory := t.TempDir()
	app := bootstrappedApp(t, dataDirectory)

	// Build the legacy schema by hand: the current reminders table, the NOT
	// NULL audit table, its immutability triggers and its index.
	for _, statement := range []string{
		`CREATE TABLE state_reminders (
			id TEXT PRIMARY KEY,
			data_json TEXT NOT NULL CHECK(json_valid(data_json)),
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL CHECK(revision > 0),
			archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0, 1)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		) STRICT`,
		legacyAuditEventsDDL,
		`CREATE INDEX state_audit_events_reminder_sequence_idx
			ON state_audit_events(reminder_id, sequence)`,
		`CREATE TRIGGER state_audit_events_no_update
			BEFORE UPDATE ON state_audit_events
			BEGIN
				SELECT RAISE(ABORT, 'state audit events are immutable');
			END`,
		`CREATE TRIGGER state_audit_events_no_delete
			BEFORE DELETE ON state_audit_events
			BEGIN
				SELECT RAISE(ABORT, 'state audit events are immutable');
			END`,
	} {
		if _, err := app.DB().NewQuery(statement).Execute(); err != nil {
			t.Fatalf("legacy schema statement error = %v", err)
		}
	}

	// One properly sealed legacy row, so the copy path is exercised with data.
	now := time.Date(2026, time.August, 11, 18, 30, 0, 0, time.UTC)
	reminder := state.Reminder{
		ID:        "01989ec9-91ad-7000-8000-0000000000aa",
		Title:     "Legacy reminder",
		Status:    state.ReminderStatusActive,
		Revision:  1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	reminderJSON, err := json.Marshal(reminder)
	if err != nil {
		t.Fatalf("encode legacy reminder: %v", err)
	}
	if _, err := app.DB().NewQuery(`
		INSERT INTO state_reminders (id, data_json, title, description, revision, archived, created_at, updated_at)
		VALUES ({:id}, {:data_json}, {:title}, '', {:revision}, 0, {:created_at}, {:updated_at})
	`).Bind(dbx.Params{
		"id":         reminder.ID,
		"data_json":  string(reminderJSON),
		"title":      reminder.Title,
		"revision":   reminder.Revision,
		"created_at": formatTime(reminder.CreatedAt),
		"updated_at": formatTime(reminder.UpdatedAt),
	}).Execute(); err != nil {
		t.Fatalf("insert legacy reminder error = %v", err)
	}
	legacyEvent := sealLegacyEvent(t, state.AuditEvent{
		ID:              "01989ec9-91ad-7000-8000-0000000000bb",
		ReminderID:      reminder.ID,
		Action:          state.AuditActionCreated,
		Actor:           state.Actor{ID: "01989ec9-91ad-7000-8000-0000000000cc", Kind: state.ActorKindOwner},
		ServerTime:      now,
		ChangedFields:   []string{"title"},
		Revision:        1,
		CorrelationID:   "legacy-correlation",
		ClientRequestID: "legacy-request",
	}, "")
	legacyEventJSON, err := json.Marshal(legacyEvent)
	if err != nil {
		t.Fatalf("encode legacy event: %v", err)
	}
	if _, err := app.DB().NewQuery(`
		INSERT INTO state_audit_events (
			id, reminder_id, action, revision, client_request_id, previous_hash, hash, signature, server_time, event_json
		) VALUES (
			{:id}, {:reminder_id}, {:action}, {:revision}, {:client_request_id}, {:previous_hash}, {:hash}, {:signature}, {:server_time}, {:event_json}
		)
	`).Bind(dbx.Params{
		"id":                legacyEvent.ID,
		"reminder_id":       legacyEvent.ReminderID,
		"action":            string(legacyEvent.Action),
		"revision":          legacyEvent.Revision,
		"client_request_id": legacyEvent.ClientRequestID,
		"previous_hash":     legacyEvent.PreviousHash,
		"hash":              legacyEvent.Hash,
		"signature":         legacyEvent.Signature,
		"server_time":       formatTime(legacyEvent.ServerTime),
		"event_json":        string(legacyEventJSON),
	}).Execute(); err != nil {
		t.Fatalf("insert legacy audit event error = %v", err)
	}

	// Constructing the repository runs the guarded rebuild.
	repository, err := NewPocketBaseRepository(app, deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	ddl := auditSchemaDDL(t, app)
	if strings.Contains(ddl, "reminder_id TEXT NOT NULL") {
		t.Fatalf("migrated audit schema keeps reminder_id NOT NULL: %s", ddl)
	}
	assertAuditTriggers(t, app)

	// The legacy row survived the copy at its original sequence position.
	changes, err := repository.ListChanges(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	if len(changes) != 1 || changes[0].Event.ID != legacyEvent.ID || changes[0].Cursor != 1 {
		t.Fatalf("changes after migration = %#v", changes)
	}

	// New NULL-reminder events chain onto the copied row, and the whole chain
	// still verifies.
	service := state.NewService(repository, state.WithClock(func() time.Time { return executionNow }))
	project, err := service.CreateProject(context.Background(), executorOwner, state.CreateProjectInput{
		Name:            "customer-api",
		ClientRequestID: execRequestID(),
	})
	if err != nil {
		t.Fatalf("CreateProject() after migration error = %v", err)
	}
	if project.ID == "" {
		t.Fatal("project ID is empty")
	}
	if err := repository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() after migration error = %v", err)
	}
	row := struct {
		ReminderID *string `db:"reminder_id"`
	}{}
	if err := app.DB().NewQuery(`
		SELECT reminder_id FROM state_audit_events WHERE action = 'project.created'
	`).One(&row); err != nil {
		t.Fatalf("read project event error = %v", err)
	}
	if row.ReminderID != nil {
		t.Fatalf("project event reminder_id = %v, want NULL", *row.ReminderID)
	}

	// The immutability triggers were recreated on the rebuilt table.
	_, updateErr := app.DB().NewQuery("UPDATE state_audit_events SET action = 'tampered' WHERE id = {:id}").
		Bind(dbx.Params{"id": legacyEvent.ID}).
		Execute()
	if updateErr == nil || !strings.Contains(updateErr.Error(), "immutable") {
		t.Fatalf("audit update error = %v, want immutable trigger error", updateErr)
	}
	_, deleteErr := app.DB().NewQuery("DELETE FROM state_audit_events WHERE id = {:id}").
		Bind(dbx.Params{"id": legacyEvent.ID}).
		Execute()
	if deleteErr == nil || !strings.Contains(deleteErr.Error(), "immutable") {
		t.Fatalf("audit delete error = %v, want immutable trigger error", deleteErr)
	}

	// The rebuilt schema survives a restart without re-migrating.
	if err := app.ResetBootstrapState(); err != nil {
		t.Fatalf("ResetBootstrapState() error = %v", err)
	}
	restartedApp := bootstrappedApp(t, dataDirectory)
	restartedRepository, err := NewPocketBaseRepository(restartedApp, deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() after restart error = %v", err)
	}
	if err := restartedRepository.VerifyAuditChain(context.Background()); err != nil {
		t.Fatalf("VerifyAuditChain() after restart error = %v", err)
	}
}

// sealLegacyEvent replicates the repository's seal for a hand-built legacy
// fixture row.
func sealLegacyEvent(t *testing.T, event state.AuditEvent, previousHash string) state.AuditEvent {
	t.Helper()
	event.PreviousHash = previousHash
	event.Hash = ""
	event.Signature = ""
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("encode legacy event: %v", err)
	}
	digest := sha256.Sum256(encoded)
	event.Hash = hex.EncodeToString(digest[:])
	event.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(deterministicSigningKey(), digest[:]))
	return event
}

func assertAuditTriggers(t *testing.T, app interface {
	DB() dbx.Builder
}) {
	t.Helper()
	rows := make([]struct {
		Name string `db:"name"`
	}, 0)
	if err := app.DB().NewQuery(`
		SELECT name FROM sqlite_master WHERE type = 'trigger' AND tbl_name = 'state_audit_events'
	`).All(&rows); err != nil {
		t.Fatalf("list audit triggers error = %v", err)
	}
	names := make(map[string]bool, len(rows))
	for _, row := range rows {
		names[row.Name] = true
	}
	if !names["state_audit_events_no_update"] || !names["state_audit_events_no_delete"] {
		t.Fatalf("audit triggers = %#v, want no_update and no_delete", names)
	}
}
