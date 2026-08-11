package state

import (
	"testing"
	"time"
)

func TestExpandOccurrenceSeedsClampsMonthlyRecurrenceToMonthEnd(t *testing.T) {
	t.Parallel()

	reminder := Reminder{
		ID: "01989eaa-0045-7efd-bac2-cdad2dff9d80",
		Schedule: &Schedule{
			LocalDate: "2027-01-31",
			LocalTime: "09:00",
			TimeZone:  "Europe/Copenhagen",
			Mode:      TimeZoneModeFixed,
		},
		Recurrence: &RecurrenceRule{Frequency: RecurrenceMonthly, Interval: 1},
	}
	seeds, err := ExpandOccurrenceSeeds(reminder, "2027-01-01", "2027-04-30")
	if err != nil {
		t.Fatalf("ExpandOccurrenceSeeds() error = %v", err)
	}
	want := []string{"2027-01-31", "2027-02-28", "2027-03-31", "2027-04-30"}
	if len(seeds) != len(want) {
		t.Fatalf("seed count = %d, want %d: %#v", len(seeds), len(want), seeds)
	}
	for index, date := range want {
		if seeds[index].LocalDate != date {
			t.Errorf("seed %d date = %q, want %q", index, seeds[index].LocalDate, date)
		}
	}
}

func TestExpandOccurrenceSeedsHandlesLeapDayYearlyRecurrence(t *testing.T) {
	t.Parallel()

	reminder := Reminder{
		ID: "01989eaa-0045-744b-a03d-e70e7b616773",
		Schedule: &Schedule{
			LocalDate: "2024-02-29",
			LocalTime: "12:00",
			TimeZone:  "UTC",
			Mode:      TimeZoneModeFixed,
		},
		Recurrence: &RecurrenceRule{Frequency: RecurrenceYearly, Interval: 1},
	}
	seeds, err := ExpandOccurrenceSeeds(reminder, "2024-01-01", "2028-12-31")
	if err != nil {
		t.Fatalf("ExpandOccurrenceSeeds() error = %v", err)
	}
	want := []string{"2024-02-29", "2025-02-28", "2026-02-28", "2027-02-28", "2028-02-29"}
	if len(seeds) != len(want) {
		t.Fatalf("seed count = %d, want %d: %#v", len(seeds), len(want), seeds)
	}
	for index, date := range want {
		if seeds[index].LocalDate != date {
			t.Errorf("seed %d date = %q, want %q", index, seeds[index].LocalDate, date)
		}
	}
}

func TestExpandOccurrenceSeedsPreservesWallClockAcrossDaylightSavingTime(t *testing.T) {
	t.Parallel()

	reminder := Reminder{
		ID: "01989eaa-0045-7a92-9095-eabef8780a8a",
		Schedule: &Schedule{
			LocalDate: "2026-03-28",
			LocalTime: "09:00",
			TimeZone:  "Europe/Copenhagen",
			Mode:      TimeZoneModeFloating,
		},
		Recurrence: &RecurrenceRule{Frequency: RecurrenceDaily, Interval: 1, UntilDate: "2026-03-30"},
	}
	seeds, err := ExpandOccurrenceSeeds(reminder, "2026-03-28", "2026-03-30")
	if err != nil {
		t.Fatalf("ExpandOccurrenceSeeds() error = %v", err)
	}
	if len(seeds) != 3 {
		t.Fatalf("seed count = %d, want 3", len(seeds))
	}
	for _, seed := range seeds {
		if seed.LocalTime != "09:00" || seed.ScheduledAt == nil {
			t.Fatalf("invalid wall clock seed: %#v", seed)
		}
	}
	if seeds[0].ScheduledAt.UTC().Hour() != 8 {
		t.Fatalf("pre-DST UTC hour = %d, want 8", seeds[0].ScheduledAt.UTC().Hour())
	}
	if seeds[1].ScheduledAt.UTC().Hour() != 7 {
		t.Fatalf("post-DST UTC hour = %d, want 7", seeds[1].ScheduledAt.UTC().Hour())
	}
}

func TestExpandOccurrenceSeedsSupportsDateOnlyReminder(t *testing.T) {
	t.Parallel()

	reminder := Reminder{
		ID: "01989eaa-0045-75a7-8af3-d00465558c53",
		Schedule: &Schedule{
			LocalDate: "2026-08-17",
			TimeZone:  "Europe/Copenhagen",
			Mode:      TimeZoneModeFloating,
		},
	}
	seeds, err := ExpandOccurrenceSeeds(reminder, "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatalf("ExpandOccurrenceSeeds() error = %v", err)
	}
	if len(seeds) != 1 || seeds[0].ScheduledAt != nil {
		t.Fatalf("date-only seeds = %#v", seeds)
	}
}

func TestExpandOccurrenceSeedsRejectsUnknownTimeZone(t *testing.T) {
	t.Parallel()

	reminder := Reminder{
		Schedule: &Schedule{
			LocalDate: "2026-08-17",
			LocalTime: "09:00",
			TimeZone:  "Mars/Olympus",
			Mode:      TimeZoneModeFixed,
		},
	}
	_, err := ExpandOccurrenceSeeds(reminder, "2026-08-01", "2026-08-31")
	if err == nil {
		t.Fatal("ExpandOccurrenceSeeds() succeeded with unknown time zone")
	}
}

func assertTimeEqual(t *testing.T, got *time.Time, want time.Time) {
	t.Helper()
	if got == nil || !got.Equal(want) {
		t.Fatalf("time = %v, want %v", got, want)
	}
}
