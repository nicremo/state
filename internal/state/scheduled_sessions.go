package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// dueCandidate is the newest due occurrence of a reminder and the older due
// occurrences it stands for.
type dueCandidate struct {
	Reminder   Reminder
	Occurrence Occurrence
	Earlier    []Occurrence
}

// newestDuePerReminder keeps, per reminder, the last occurrence of the list
// (the repository orders by local date and time) and remembers the earlier
// ones, in the order the reminders first appear.
func newestDuePerReminder(due []DueOccurrence) []dueCandidate {
	order := make([]string, 0)
	byReminder := make(map[string]*dueCandidate)
	for _, item := range due {
		candidate, seen := byReminder[item.Reminder.ID]
		if !seen {
			order = append(order, item.Reminder.ID)
			byReminder[item.Reminder.ID] = &dueCandidate{Reminder: item.Reminder, Occurrence: item.Occurrence}
			continue
		}
		candidate.Earlier = append(candidate.Earlier, candidate.Occurrence)
		candidate.Reminder = item.Reminder
		candidate.Occurrence = item.Occurrence
	}
	result := make([]dueCandidate, 0, len(order))
	for _, id := range order {
		result = append(result, *byReminder[id])
	}
	return result
}

// maxListedEarlierOccurrences bounds the dates a scheduled session lists.
const maxListedEarlierOccurrences = 5

// startScheduledSession opens the agent session for a due occurrence. The
// first round carries the occurrence, so a successful round completes it
// like a scheduled run, and the reminder's model. A repeated cycle finds the
// stored session by its idempotency key and creates nothing.
func (service *Service) startScheduledSession(ctx context.Context, reminder Reminder, occurrence Occurrence, earlier []Occurrence, policy ExecutionPolicy, project Project, now time.Time) (AgentRun, bool, error) {
	key := fmt.Sprintf("materialize:%s:%s:%d", occurrence.ID, policy.ID, policy.Revision)
	sessionID, err := service.newID()
	if err != nil {
		return AgentRun{}, false, fmt.Errorf("generate session ID: %w", err)
	}
	actor := SystemActor()
	session := AgentSession{
		ID:             sessionID,
		ReminderID:     reminder.ID,
		PolicyID:       policy.ID,
		ProjectID:      project.ID,
		ProjectName:    project.Name,
		Adapter:        policy.Adapter,
		Model:          cloneAgentModel(reminder.ExecutionModel),
		Title:          reminder.Title,
		CreatedByActor: actor,
		Revision:       1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if !validAgentModelPointer(session.Model) {
		session.Model = nil
	}
	metadata := MutationMetadata{ClientRequestID: key, Source: "scheduler"}
	objective := scheduledObjective(reminder, occurrence, earlier)
	first, firstEvent, err := service.newSessionTurn(ctx, actor, session, policy, project, TurnKindMessage, objective, "", "", metadata)
	if err != nil {
		return AgentRun{}, false, err
	}
	occurrenceID := occurrence.ID
	first.OccurrenceID = &occurrenceID
	eventID, err := service.newID()
	if err != nil {
		return AgentRun{}, false, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildAuditEvent(eventID, reminder.ID, AuditActionAgentSessionStarted, actor, now, nil, "scheduler", "", nil, session, []string{"adapter", "model", "policy_id", "project_id", "title"}, session.Revision, "", key)
	if err != nil {
		return AgentRun{}, false, err
	}
	_, created, err := service.repository.CreateAgentSession(ctx, session, event, first, firstEvent, key)
	if errors.Is(err, ErrRunStateConflict) {
		// The occurrence already has a run for this policy revision.
		return AgentRun{}, false, nil
	}
	if err != nil {
		return AgentRun{}, false, err
	}
	return first, created, nil
}

// scheduledObjective is the first round of a scheduled session: the task as
// a manual start reads it, plus when it was due and which older dates of
// the same reminder were missed and not started on their own.
func scheduledObjective(reminder Reminder, occurrence Occurrence, earlier []Occurrence) string {
	var builder strings.Builder
	builder.WriteString(firstRoundObjective(reminder, ""))
	builder.WriteString("\n\nStarted by State because the reminder is due. Due: " + occurrenceLabel(occurrence))
	if len(earlier) > 0 {
		dates := make([]string, 0, maxListedEarlierOccurrences)
		// Newest first: the most recent missed dates matter most.
		for index := len(earlier) - 1; index >= 0 && len(dates) < maxListedEarlierOccurrences; index-- {
			dates = append(dates, earlier[index].LocalDate)
		}
		builder.WriteString("\nAlso due and not started automatically: " + strings.Join(dates, ", "))
		if extra := len(earlier) - len(dates); extra > 0 {
			fmt.Fprintf(&builder, " (+%d more)", extra)
		}
		builder.WriteString(". Decide whether this round has to cover them too, for example a missed monthly report, and say so in your summary.")
	}
	return builder.String()
}

func occurrenceLabel(occurrence Occurrence) string {
	label := occurrence.LocalDate
	if occurrence.LocalTime != "" {
		label += " " + occurrence.LocalTime
	}
	if occurrence.TimeZone != "" {
		label += " (" + occurrence.TimeZone + ")"
	}
	return label
}
