package mcpserver

import (
	"fmt"
	"testing"
	"time"

	"github.com/nicremo/state/internal/state"
)

func TestBoundReminderDetailKeepsCommentsAndTrimsTheRest(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 7, 12, 0, 0, 0, time.UTC)
	occurrences := make([]state.Occurrence, 0)
	for day := -10; day < 10; day++ {
		at := now.AddDate(0, 0, day)
		occurrences = append(occurrences, state.Occurrence{ID: fmt.Sprint(day), LocalDate: at.Format("2006-01-02"), LocalTime: "09:00", ScheduledAt: &at, Status: state.OccurrenceStatusPending})
	}
	history := make([]state.AuditEvent, 30)
	comments := make([]state.Comment, 12)
	detail := map[string]any{"occurrences": occurrences, "history": history, "comments": comments}

	boundReminderDetail(detail, now)

	kept := detail["occurrences"].([]state.Occurrence)
	if len(kept) != boundedPastOccurrences+boundedUpcomingOccurrences {
		t.Fatalf("occurrences = %d", len(kept))
	}
	if kept[0].ID != "-2" || kept[len(kept)-1].ID != "5" {
		t.Fatalf("window = %s .. %s", kept[0].ID, kept[len(kept)-1].ID)
	}
	if len(detail["history"].([]state.AuditEvent)) != boundedHistoryEntries || len(detail["comments"].([]state.Comment)) != 12 {
		t.Fatalf("history or comments trimmed wrongly")
	}
	bounded := detail["bounded"].(map[string]any)
	if bounded["occurrences_total"] != 20 || bounded["history_total"] != 30 {
		t.Fatalf("bounded = %+v", bounded)
	}
}
