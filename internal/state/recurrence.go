package state

import (
	"fmt"
	"time"
)

const localDateLayout = "2006-01-02"

func ExpandOccurrenceSeeds(reminder Reminder, fromLocalDate string, throughLocalDate string) ([]OccurrenceSeed, error) {
	if reminder.Schedule == nil {
		return []OccurrenceSeed{}, nil
	}
	from, err := time.Parse(localDateLayout, fromLocalDate)
	if err != nil {
		return nil, fmt.Errorf("parse occurrence range start: %w", err)
	}
	through, err := time.Parse(localDateLayout, throughLocalDate)
	if err != nil {
		return nil, fmt.Errorf("parse occurrence range end: %w", err)
	}
	if through.Before(from) {
		return nil, ErrInvalidInput
	}
	start, err := time.Parse(localDateLayout, reminder.Schedule.LocalDate)
	if err != nil {
		return nil, fmt.Errorf("parse schedule date: %w", err)
	}
	if reminder.Schedule.Mode != TimeZoneModeFixed && reminder.Schedule.Mode != TimeZoneModeFloating {
		return nil, ErrInvalidInput
	}
	location, err := time.LoadLocation(reminder.Schedule.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("load schedule time zone: %w", err)
	}
	hour, minute, err := parseLocalTime(reminder.Schedule.LocalTime)
	if err != nil {
		return nil, err
	}
	var until *time.Time
	if reminder.Recurrence != nil && reminder.Recurrence.UntilDate != "" {
		parsedUntil, err := time.Parse(localDateLayout, reminder.Recurrence.UntilDate)
		if err != nil {
			return nil, fmt.Errorf("parse recurrence end: %w", err)
		}
		until = &parsedUntil
	}

	appendSeed := func(seeds []OccurrenceSeed, date time.Time) []OccurrenceSeed {
		if date.Before(from) || date.After(through) || (until != nil && date.After(*until)) {
			return seeds
		}
		seed := OccurrenceSeed{
			LocalDate:         date.Format(localDateLayout),
			LocalTime:         reminder.Schedule.LocalTime,
			TimeZone:          reminder.Schedule.TimeZone,
			TimeZoneMode:      reminder.Schedule.Mode,
			PrewarningMinutes: reminder.Schedule.PrewarningMinutes,
		}
		if reminder.Schedule.LocalTime != "" {
			scheduledAt := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, location)
			seed.ScheduledAt = &scheduledAt
		}
		return append(seeds, seed)
	}

	if reminder.Recurrence == nil {
		return appendSeed([]OccurrenceSeed{}, start), nil
	}
	interval := reminder.Recurrence.Interval
	if interval <= 0 {
		return nil, ErrInvalidInput
	}
	seeds := make([]OccurrenceSeed, 0)
	for index := 0; index < 10000; index++ {
		date, err := recurrenceDate(start, reminder.Recurrence.Frequency, interval, index)
		if err != nil {
			return nil, err
		}
		if date.After(through) || (until != nil && date.After(*until)) {
			break
		}
		seeds = appendSeed(seeds, date)
	}
	return seeds, nil
}

func recurrenceDate(start time.Time, frequency RecurrenceFrequency, interval int, index int) (time.Time, error) {
	switch frequency {
	case RecurrenceDaily:
		return start.AddDate(0, 0, index*interval), nil
	case RecurrenceWeekly:
		return start.AddDate(0, 0, index*interval*7), nil
	case RecurrenceMonthly:
		monthIndex := int(start.Month()) - 1 + index*interval
		year := start.Year() + monthIndex/12
		month := time.Month(monthIndex%12 + 1)
		return clampedDate(year, month, start.Day()), nil
	case RecurrenceYearly:
		return clampedDate(start.Year()+index*interval, start.Month(), start.Day()), nil
	default:
		return time.Time{}, ErrInvalidInput
	}
}

func clampedDate(year int, month time.Month, day int) time.Time {
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func parseLocalTime(value string) (int, int, error) {
	if value == "" {
		return 0, 0, nil
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, 0, fmt.Errorf("parse schedule time: %w", err)
	}
	return parsed.Hour(), parsed.Minute(), nil
}
