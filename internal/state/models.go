package state

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrRevisionConflict = errors.New("revision conflict")
	ErrForbidden        = errors.New("forbidden")
	ErrInvalidInput     = errors.New("invalid input")
)

type ActorKind string

const (
	ActorKindOwner   ActorKind = "owner"
	ActorKindDevice  ActorKind = "device"
	ActorKindHarness ActorKind = "harness"
	ActorKindSystem  ActorKind = "system"
)

type Actor struct {
	ID          string    `json:"id"`
	Kind        ActorKind `json:"kind"`
	DisplayName string    `json:"display_name,omitempty"`
	Harness     string    `json:"harness,omitempty"`
	DeviceName  string    `json:"device_name,omitempty"`
}

type ReminderStatus string

const (
	ReminderStatusActive    ReminderStatus = "active"
	ReminderStatusCompleted ReminderStatus = "completed"
)

type TimeZoneMode string

const (
	TimeZoneModeFloating TimeZoneMode = "floating"
	TimeZoneModeFixed    TimeZoneMode = "fixed"
)

type Schedule struct {
	LocalDate         string       `json:"local_date"`
	LocalTime         string       `json:"local_time,omitempty"`
	TimeZone          string       `json:"time_zone"`
	Mode              TimeZoneMode `json:"mode"`
	PrewarningMinutes int          `json:"prewarning_minutes,omitempty"`
}

type RecurrenceFrequency string

const (
	RecurrenceDaily   RecurrenceFrequency = "daily"
	RecurrenceWeekly  RecurrenceFrequency = "weekly"
	RecurrenceMonthly RecurrenceFrequency = "monthly"
	RecurrenceYearly  RecurrenceFrequency = "yearly"
)

type RecurrenceRule struct {
	Frequency RecurrenceFrequency `json:"frequency"`
	Interval  int                 `json:"interval"`
	UntilDate string              `json:"until_date,omitempty"`
}

type OccurrenceSeed struct {
	LocalDate         string       `json:"local_date"`
	LocalTime         string       `json:"local_time,omitempty"`
	TimeZone          string       `json:"time_zone"`
	TimeZoneMode      TimeZoneMode `json:"time_zone_mode"`
	PrewarningMinutes int          `json:"prewarning_minutes,omitempty"`
	ScheduledAt       *time.Time   `json:"scheduled_at,omitempty"`
}

type OccurrenceStatus string

const (
	OccurrenceStatusPending   OccurrenceStatus = "pending"
	OccurrenceStatusCompleted OccurrenceStatus = "completed"
	OccurrenceStatusSnoozed   OccurrenceStatus = "snoozed"
)

type Occurrence struct {
	ID                string           `json:"id"`
	ReminderID        string           `json:"reminder_id"`
	LocalDate         string           `json:"local_date"`
	LocalTime         string           `json:"local_time,omitempty"`
	TimeZone          string           `json:"time_zone"`
	TimeZoneMode      TimeZoneMode     `json:"time_zone_mode"`
	PrewarningMinutes int              `json:"prewarning_minutes,omitempty"`
	ScheduledAt       *time.Time       `json:"scheduled_at,omitempty"`
	Status            OccurrenceStatus `json:"status"`
	CompletedAt       *time.Time       `json:"completed_at,omitempty"`
	SnoozedUntil      *time.Time       `json:"snoozed_until,omitempty"`
	Revision          int64            `json:"revision"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

type Reminder struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Status      ReminderStatus  `json:"status"`
	Schedule    *Schedule       `json:"schedule,omitempty"`
	Recurrence  *RecurrenceRule `json:"recurrence,omitempty"`
	Revision    int64           `json:"revision"`
	Archived    bool            `json:"archived"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type Comment struct {
	ID         string    `json:"id"`
	ReminderID string    `json:"reminder_id"`
	Body       string    `json:"body"`
	Actor      Actor     `json:"actor"`
	Revision   int64     `json:"revision"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type AuditAction string

const (
	AuditActionCreated           AuditAction = "reminder.created"
	AuditActionUpdated           AuditAction = "reminder.updated"
	AuditActionArchived          AuditAction = "reminder.archived"
	AuditActionRestored          AuditAction = "reminder.restored"
	AuditActionCommentAdded      AuditAction = "comment.added"
	AuditActionOccurrenceDone    AuditAction = "occurrence.completed"
	AuditActionOccurrenceSnoozed AuditAction = "occurrence.snoozed"
	AuditActionConflictResolved  AuditAction = "conflict.resolved"
)

type AuditEvent struct {
	ID              string          `json:"id"`
	ReminderID      string          `json:"reminder_id"`
	Action          AuditAction     `json:"action"`
	Actor           Actor           `json:"actor"`
	ServerTime      time.Time       `json:"server_time"`
	ClientTime      *time.Time      `json:"client_time,omitempty"`
	Source          string          `json:"source,omitempty"`
	SourceExcerpt   string          `json:"source_excerpt,omitempty"`
	BeforeSnapshot  json.RawMessage `json:"before_snapshot,omitempty"`
	AfterSnapshot   json.RawMessage `json:"after_snapshot,omitempty"`
	ChangedFields   []string        `json:"changed_fields"`
	Revision        int64           `json:"revision"`
	CorrelationID   string          `json:"correlation_id"`
	ClientRequestID string          `json:"client_request_id"`
	PreviousHash    string          `json:"previous_hash,omitempty"`
	Hash            string          `json:"hash"`
	Signature       string          `json:"signature"`
}

type CreateReminderInput struct {
	Title           string          `json:"title"`
	Description     string          `json:"description,omitempty"`
	Schedule        *Schedule       `json:"schedule,omitempty"`
	Recurrence      *RecurrenceRule `json:"recurrence,omitempty"`
	ClientTime      *time.Time      `json:"client_time,omitempty"`
	Source          string          `json:"source,omitempty"`
	SourceExcerpt   string          `json:"source_excerpt,omitempty"`
	ClientRequestID string          `json:"client_request_id"`
	CorrelationID   string          `json:"correlation_id,omitempty"`
}

type UpdateReminderInput struct {
	Title            *string          `json:"title,omitempty"`
	Description      *string          `json:"description,omitempty"`
	Status           *ReminderStatus  `json:"status,omitempty"`
	Schedule         **Schedule       `json:"schedule,omitempty"`
	Recurrence       **RecurrenceRule `json:"recurrence,omitempty"`
	Archived         *bool            `json:"archived,omitempty"`
	ExpectedRevision int64            `json:"expected_revision"`
	ClientTime       *time.Time       `json:"client_time,omitempty"`
	Source           string           `json:"source,omitempty"`
	SourceExcerpt    string           `json:"source_excerpt,omitempty"`
	ClientRequestID  string           `json:"client_request_id"`
	CorrelationID    string           `json:"correlation_id,omitempty"`
}

type AddCommentInput struct {
	Body            string     `json:"body"`
	ClientTime      *time.Time `json:"client_time,omitempty"`
	Source          string     `json:"source,omitempty"`
	SourceExcerpt   string     `json:"source_excerpt,omitempty"`
	ClientRequestID string     `json:"client_request_id"`
	CorrelationID   string     `json:"correlation_id,omitempty"`
}

type OccurrenceListOptions struct {
	Status *OccurrenceStatus
	Limit  int
}

type CompleteOccurrenceInput struct {
	ExpectedRevision int64      `json:"expected_revision"`
	ClientTime       *time.Time `json:"client_time,omitempty"`
	Source           string     `json:"source,omitempty"`
	SourceExcerpt    string     `json:"source_excerpt,omitempty"`
	ClientRequestID  string     `json:"client_request_id"`
	CorrelationID    string     `json:"correlation_id,omitempty"`
}

type SnoozeOccurrenceInput struct {
	Until            time.Time  `json:"until"`
	ExpectedRevision int64      `json:"expected_revision"`
	ClientTime       *time.Time `json:"client_time,omitempty"`
	Source           string     `json:"source,omitempty"`
	SourceExcerpt    string     `json:"source_excerpt,omitempty"`
	ClientRequestID  string     `json:"client_request_id"`
	CorrelationID    string     `json:"correlation_id,omitempty"`
}

type ReminderListOptions struct {
	IncludeArchived bool
	Status          *ReminderStatus
	Limit           int
}

type Change struct {
	Cursor int64      `json:"cursor"`
	Event  AuditEvent `json:"event"`
}

type BriefingOptions struct {
	AfterCursor int64
	Limit       int
}

type Briefing struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Cursor      int64      `json:"cursor"`
	Summary     string     `json:"summary"`
	Reminders   []Reminder `json:"reminders"`
	Changes     []Change   `json:"changes"`
}
