package state

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// An agent session is a conversation with a coding agent on the owner's
// Mac. The owner starts it from a reminder; every message is one round, an
// ordinary AgentRun that a runner claims and executes like any other run.
// The runner starts the agent CLI without a terminal, the first round fresh
// and every later one with the CLI's resume flag, and reports the agent's
// final message together with the CLI's session ID. Between rounds the
// session waits for the owner.

type SessionStatus string

const (
	SessionStatusWorking       SessionStatus = "working"
	SessionStatusNeedsApproval SessionStatus = "needs_approval"
	SessionStatusWaiting       SessionStatus = "waiting"
	SessionStatusFailed        SessionStatus = "failed"
	SessionStatusClosed        SessionStatus = "closed"
)

type TurnKind string

const (
	// TurnKindMessage sends the owner's message to the agent.
	TurnKindMessage TurnKind = "message"
	// TurnKindOpenTerminal opens the agent's session in a terminal on the
	// Mac, so the owner can continue there.
	TurnKindOpenTerminal TurnKind = "open_terminal"
)

const (
	AuditActionAgentSessionStarted AuditAction = "agent_session.started"
	AuditActionAgentSessionClosed  AuditAction = "agent_session.closed"

	// MaxSessionMessageRunes bounds one message of the owner.
	MaxSessionMessageRunes = 8000
	// MaxRunResultTextRunes bounds the agent's final message of a round.
	MaxRunResultTextRunes = 20000
	maxSessionTurns       = 500
)

var harnessSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

// ValidHarnessSessionID reports whether an agent CLI session ID is safe to
// store and to pass back to the CLI as an argument.
func ValidHarnessSessionID(id string) bool {
	return harnessSessionIDPattern.MatchString(id)
}

type AgentSession struct {
	ID             string     `json:"id"`
	ReminderID     string     `json:"reminder_id"`
	PolicyID       string     `json:"policy_id"`
	ProjectID      string     `json:"project_id"`
	ProjectName    string     `json:"project_name"`
	Adapter        string     `json:"adapter"`
	Title          string     `json:"title"`
	Closed         bool       `json:"closed"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	CreatedByActor Actor      `json:"created_by_actor"`
	Revision       int64      `json:"revision"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// AgentSessionView is a session with its rounds in order and what follows
// from them.
type AgentSessionView struct {
	AgentSession
	Status SessionStatus `json:"status"`
	// HarnessSessionID is the agent CLI's session, known after the first
	// round that reported one.
	HarnessSessionID string     `json:"harness_session_id,omitempty"`
	LastActivityAt   time.Time  `json:"last_activity_at"`
	Turns            []AgentRun `json:"turns"`
}

type StartAgentSessionInput struct {
	ReminderID  string `json:"reminder_id"`
	PolicyID    string `json:"policy_id"`
	Instruction string `json:"instruction,omitempty"`
	MutationMetadata
}

type SendAgentSessionMessageInput struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	MutationMetadata
}

type AgentSessionActionInput struct {
	SessionID string `json:"session_id"`
	MutationMetadata
}

// AgentSessionRepository stores sessions. Rounds are agent runs.
type AgentSessionRepository interface {
	// CreateAgentSession stores the session and its first round at once. A
	// repeated client request returns the stored session with created false.
	CreateAgentSession(ctx context.Context, session AgentSession, event AuditEvent, first AgentRun, firstEvent AuditEvent, clientRequestID string) (AgentSession, bool, error)
	// CreateAgentSessionTurn adds a round to an open session that has no
	// round in progress, and fails with ErrRunStateConflict otherwise.
	CreateAgentSessionTurn(ctx context.Context, run AgentRun, event AuditEvent, clientRequestID string) (AgentRun, bool, error)
	GetAgentSession(ctx context.Context, sessionID string) (AgentSession, error)
	ListAgentSessions(ctx context.Context, limit int) ([]AgentSession, error)
	UpdateAgentSession(ctx context.Context, session AgentSession, expectedRevision int64, event AuditEvent, clientRequestID string) (AgentSession, error)
}

func canDriveSessions(actor Actor) bool {
	return actor.Kind == ActorKindOwner || actor.Kind == ActorKindDevice
}

// StartAgentSession starts an agent on the reminder's task with the policy's
// project, agent and rights.
func (service *Service) StartAgentSession(ctx context.Context, actor Actor, input StartAgentSessionInput) (AgentSessionView, error) {
	if !canDriveSessions(actor) {
		return AgentSessionView{}, ErrForbidden
	}
	instruction := strings.TrimSpace(input.Instruction)
	if actor.ID == "" || input.ReminderID == "" || input.PolicyID == "" || input.ClientRequestID == "" || utf8.RuneCountInString(instruction) > MaxSessionMessageRunes {
		return AgentSessionView{}, ErrInvalidInput
	}
	reminder, err := service.repository.GetReminder(ctx, input.ReminderID)
	if err != nil {
		return AgentSessionView{}, err
	}
	policy, err := service.repository.GetPolicy(ctx, input.PolicyID)
	if err != nil {
		return AgentSessionView{}, err
	}
	if !policy.Enabled {
		return AgentSessionView{}, ErrInvalidInput
	}
	project, err := service.repository.GetProject(ctx, policy.ProjectID)
	if err != nil {
		return AgentSessionView{}, err
	}
	sessionID, err := service.newID()
	if err != nil {
		return AgentSessionView{}, fmt.Errorf("generate session ID: %w", err)
	}
	now := service.clock().UTC()
	session := AgentSession{
		ID:             sessionID,
		ReminderID:     reminder.ID,
		PolicyID:       policy.ID,
		ProjectID:      project.ID,
		ProjectName:    project.Name,
		Adapter:        policy.Adapter,
		Title:          reminder.Title,
		CreatedByActor: actor,
		Revision:       1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	first, firstEvent, err := service.newSessionTurn(ctx, actor, session, policy, project, TurnKindMessage, firstRoundObjective(reminder, instruction), instruction, "", input.MutationMetadata)
	if err != nil {
		return AgentSessionView{}, err
	}
	eventID, err := service.newID()
	if err != nil {
		return AgentSessionView{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildAuditEvent(eventID, reminder.ID, AuditActionAgentSessionStarted, actor, now, input.ClientTime, input.Source, firstLine(instruction), nil, session, []string{"adapter", "policy_id", "project_id", "title"}, session.Revision, input.CorrelationID, input.ClientRequestID)
	if err != nil {
		return AgentSessionView{}, err
	}
	stored, _, err := service.repository.CreateAgentSession(ctx, session, event, first, firstEvent, input.ClientRequestID)
	if err != nil {
		return AgentSessionView{}, err
	}
	return service.GetAgentSession(ctx, stored.ID)
}

// firstRoundObjective is what the agent reads in the first round: the task
// from the reminder, the owner's instruction and how a round ends.
func firstRoundObjective(reminder Reminder, instruction string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Task from State: %s\n", reminder.Title)
	if description := strings.TrimSpace(reminder.Description); description != "" {
		builder.WriteString("\n" + description + "\n")
	}
	if instruction != "" {
		builder.WriteString("\nThe owner's instruction:\n" + instruction + "\n")
	}
	builder.WriteString("\n" + sessionRules)
	return builder.String()
}

// sessionRules tells the agent how a round works. The owner reads only the
// final message of each round, in the State app, and answers there.
const sessionRules = `You work in a State session on the owner's Mac. The owner is not watching the terminal: they read only your final message of each round, in the State app, and answer there, which starts your next round. Do the work you can do now, then end the round with a short Markdown summary in the owner's language: what you did, what is open, and any question you need answered. Never wait for input inside a round.`

// SendAgentSessionMessage starts the next round with the owner's message.
func (service *Service) SendAgentSessionMessage(ctx context.Context, actor Actor, input SendAgentSessionMessageInput) (AgentSessionView, error) {
	if !canDriveSessions(actor) {
		return AgentSessionView{}, ErrForbidden
	}
	text := strings.TrimSpace(input.Text)
	if input.SessionID == "" || input.ClientRequestID == "" || text == "" || utf8.RuneCountInString(text) > MaxSessionMessageRunes {
		return AgentSessionView{}, ErrInvalidInput
	}
	return service.addSessionTurn(ctx, actor, input.SessionID, TurnKindMessage, text, input.MutationMetadata)
}

// OpenAgentSessionOnMac asks the runner to open the agent's session in a
// terminal on the Mac, so the owner can continue there.
func (service *Service) OpenAgentSessionOnMac(ctx context.Context, actor Actor, input AgentSessionActionInput) (AgentSessionView, error) {
	if !canDriveSessions(actor) {
		return AgentSessionView{}, ErrForbidden
	}
	if input.SessionID == "" || input.ClientRequestID == "" {
		return AgentSessionView{}, ErrInvalidInput
	}
	return service.addSessionTurn(ctx, actor, input.SessionID, TurnKindOpenTerminal, "", input.MutationMetadata)
}

func (service *Service) addSessionTurn(ctx context.Context, actor Actor, sessionID string, kind TurnKind, text string, metadata MutationMetadata) (AgentSessionView, error) {
	view, err := service.GetAgentSession(ctx, sessionID)
	if err != nil {
		return AgentSessionView{}, err
	}
	if view.Closed || view.hasActiveTurn() {
		return AgentSessionView{}, ErrRunStateConflict
	}
	if kind == TurnKindOpenTerminal && view.HarnessSessionID == "" {
		return AgentSessionView{}, ErrInvalidInput
	}
	policy, err := service.repository.GetPolicy(ctx, view.PolicyID)
	if err != nil {
		return AgentSessionView{}, err
	}
	if !policy.Enabled {
		return AgentSessionView{}, ErrInvalidInput
	}
	project, err := service.repository.GetProject(ctx, view.ProjectID)
	if err != nil {
		return AgentSessionView{}, err
	}
	objective := text
	if kind == TurnKindOpenTerminal {
		objective = "Open this session in a terminal on the Mac."
	}
	run, event, err := service.newSessionTurn(ctx, actor, view.AgentSession, policy, project, kind, objective, text, view.HarnessSessionID, metadata)
	if err != nil {
		return AgentSessionView{}, err
	}
	if _, _, err := service.repository.CreateAgentSessionTurn(ctx, run, event, metadata.ClientRequestID); err != nil {
		return AgentSessionView{}, err
	}
	return service.GetAgentSession(ctx, sessionID)
}

// newSessionTurn builds one round and its audit event.
func (service *Service) newSessionTurn(ctx context.Context, actor Actor, session AgentSession, policy ExecutionPolicy, project Project, kind TurnKind, objective string, prompt string, resumeID string, metadata MutationMetadata) (AgentRun, AuditEvent, error) {
	runID, err := service.newID()
	if err != nil {
		return AgentRun{}, AuditEvent{}, fmt.Errorf("generate run ID: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return AgentRun{}, AuditEvent{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	cursor, err := service.repository.LatestChangeCursor(ctx)
	if err != nil {
		return AgentRun{}, AuditEvent{}, err
	}
	now := service.clock().UTC()
	contract := TaskContract{
		RunID:               runID,
		CorrelationID:       session.ID,
		Objective:           objective,
		ProjectID:           project.ID,
		ProjectName:         project.Name,
		PolicyID:            policy.ID,
		PolicyRevision:      policy.Revision,
		AllowedCapabilities: append([]string(nil), policy.AllowedCapabilities...),
		TimeoutMinutes:      policy.TimeoutMinutes,
		SessionID:           session.ID,
		TurnKind:            string(kind),
		ResumeSessionID:     resumeID,
	}
	contract.ContractHash = contract.ComputeHash()
	run := AgentRun{
		ID:             runID,
		ReminderID:     session.ReminderID,
		PolicyID:       policy.ID,
		PolicyRevision: policy.Revision,
		ProjectID:      project.ID,
		Adapter:        policy.Adapter,
		Status:         AgentRunStatusEligible,
		IdempotencyKey: "session:" + session.ID + ":" + runID,
		TaskContract:   contract,
		ContextCursor:  cursor,
		RequestedAt:    &now,
		SessionID:      session.ID,
		TurnKind:       kind,
		Prompt:         prompt,
		CreatedByActor: actor,
		Revision:       1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	event, err := service.buildRunEvent(eventID, run, AuditActionRunEligible, actor, now, metadata.ClientTime, metadata.Source, firstLine(prompt), nil, run, []string{"adapter", "context_cursor", "policy_id", "policy_revision", "project_id", "reminder_id", "session_id", "status", "task_contract"}, "session-turn:"+runID)
	if err != nil {
		return AgentRun{}, AuditEvent{}, err
	}
	return run, event, nil
}

// CloseAgentSession ends a session. A round in progress has to be cancelled
// first.
func (service *Service) CloseAgentSession(ctx context.Context, actor Actor, input AgentSessionActionInput) (AgentSessionView, error) {
	if !canDriveSessions(actor) {
		return AgentSessionView{}, ErrForbidden
	}
	if input.SessionID == "" || input.ClientRequestID == "" {
		return AgentSessionView{}, ErrInvalidInput
	}
	view, err := service.GetAgentSession(ctx, input.SessionID)
	if err != nil {
		return AgentSessionView{}, err
	}
	if view.Closed {
		return view, nil
	}
	if view.hasActiveTurn() {
		return AgentSessionView{}, ErrRunStateConflict
	}
	now := service.clock().UTC()
	closed := view.AgentSession
	closed.Closed = true
	closed.ClosedAt = &now
	closed.Revision++
	closed.UpdatedAt = now
	eventID, err := service.newID()
	if err != nil {
		return AgentSessionView{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildAuditEvent(eventID, closed.ReminderID, AuditActionAgentSessionClosed, actor, now, input.ClientTime, input.Source, input.SourceExcerpt, view.AgentSession, closed, []string{"closed", "closed_at"}, closed.Revision, input.CorrelationID, input.ClientRequestID)
	if err != nil {
		return AgentSessionView{}, err
	}
	if _, err := service.repository.UpdateAgentSession(ctx, closed, view.Revision, event, input.ClientRequestID); err != nil {
		return AgentSessionView{}, err
	}
	return service.GetAgentSession(ctx, input.SessionID)
}

func (service *Service) GetAgentSession(ctx context.Context, sessionID string) (AgentSessionView, error) {
	if sessionID == "" {
		return AgentSessionView{}, ErrInvalidInput
	}
	session, err := service.repository.GetAgentSession(ctx, sessionID)
	if err != nil {
		return AgentSessionView{}, err
	}
	return service.sessionView(ctx, session)
}

// ListAgentSessions returns the sessions with the most recent activity first.
func (service *Service) ListAgentSessions(ctx context.Context, limit int) ([]AgentSessionView, error) {
	sessions, err := service.repository.ListAgentSessions(ctx, limit)
	if err != nil {
		return nil, err
	}
	views := make([]AgentSessionView, 0, len(sessions))
	for _, session := range sessions {
		view, err := service.sessionView(ctx, session)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	sort.SliceStable(views, func(left, right int) bool {
		return views[left].LastActivityAt.After(views[right].LastActivityAt)
	})
	return views, nil
}

func (service *Service) sessionView(ctx context.Context, session AgentSession) (AgentSessionView, error) {
	turns, err := service.repository.ListAgentRuns(ctx, AgentRunListFilter{SessionID: session.ID, Limit: maxSessionTurns})
	if err != nil {
		return AgentSessionView{}, err
	}
	sort.SliceStable(turns, func(left, right int) bool {
		if !turns[left].CreatedAt.Equal(turns[right].CreatedAt) {
			return turns[left].CreatedAt.Before(turns[right].CreatedAt)
		}
		return turns[left].ID < turns[right].ID
	})
	view := AgentSessionView{AgentSession: session, Turns: turns, LastActivityAt: session.UpdatedAt}
	for _, turn := range turns {
		if view.HarnessSessionID == "" && turn.HarnessSessionID != "" {
			view.HarnessSessionID = turn.HarnessSessionID
		}
		if turn.UpdatedAt.After(view.LastActivityAt) {
			view.LastActivityAt = turn.UpdatedAt
		}
	}
	view.Status = deriveSessionStatus(session, turns)
	return view, nil
}

func (view AgentSessionView) hasActiveTurn() bool {
	for _, turn := range view.Turns {
		if !turn.Terminal() {
			return true
		}
	}
	return false
}

// deriveSessionStatus follows the latest round: a round in progress means
// working, otherwise the last message round decides whether the agent
// failed or waits for the owner.
func deriveSessionStatus(session AgentSession, turns []AgentRun) SessionStatus {
	if session.Closed {
		return SessionStatusClosed
	}
	for index := len(turns) - 1; index >= 0; index-- {
		turn := turns[index]
		if turn.Status == AgentRunStatusNeedsApproval {
			return SessionStatusNeedsApproval
		}
		if !turn.Terminal() {
			return SessionStatusWorking
		}
	}
	for index := len(turns) - 1; index >= 0; index-- {
		turn := turns[index]
		if turn.TurnKind == TurnKindOpenTerminal {
			continue
		}
		if turn.Status == AgentRunStatusFailed || turn.Status == AgentRunStatusExpired {
			return SessionStatusFailed
		}
		return SessionStatusWaiting
	}
	return SessionStatusWaiting
}

// redactResultText removes credential-looking lines from an agent's final
// message and bounds it.
func redactResultText(text string) string {
	lines := strings.Split(strings.ToValidUTF8(text, ""), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if !secretLinePattern.MatchString(line) {
			kept = append(kept, line)
		}
	}
	redacted := strings.TrimSpace(strings.Join(kept, "\n"))
	if runes := []rune(redacted); len(runes) > MaxRunResultTextRunes {
		redacted = string(runes[:MaxRunResultTextRunes])
	}
	return redacted
}
