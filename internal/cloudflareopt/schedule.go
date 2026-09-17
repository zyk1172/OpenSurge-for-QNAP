package cloudflareopt

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cronField struct {
	values     map[int]bool
	wildcard   bool
}

type cronSpec struct {
	minute cronField
	hour   cronField
	dom    cronField
	month  cronField
	dow    cronField
}

func ValidateSchedule(schedule Schedule) error {
	switch schedule.Mode {
	case ScheduleInterval:
		if schedule.EveryDays < 1 || schedule.EveryDays > 365 {
			return fmt.Errorf("schedule every_days must be between 1 and 365")
		}
		if _, _, err := parseClock(schedule.At); err != nil {
			return fmt.Errorf("schedule at: %w", err)
		}
		return nil
	case ScheduleCron:
		if _, err := parseCron(schedule.Cron); err != nil {
			return fmt.Errorf("schedule cron: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("schedule mode must be %q or %q", ScheduleInterval, ScheduleCron)
	}
}

func NextRun(schedule Schedule, now time.Time, lastRun *time.Time) (time.Time, error) {
	if err := ValidateSchedule(schedule); err != nil {
		return time.Time{}, err
	}
	switch schedule.Mode {
	case ScheduleInterval:
		hour, minute, _ := parseClock(schedule.At)
		base := now
		if lastRun != nil && !lastRun.IsZero() {
			base = lastRun.In(now.Location())
		}
		candidate := time.Date(base.Year(), base.Month(), base.Day(), hour, minute, 0, 0, now.Location()).AddDate(0, 0, schedule.EveryDays)
		for !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, schedule.EveryDays)
		}
		return candidate, nil
	case ScheduleCron:
		spec, _ := parseCron(schedule.Cron)
		return spec.next(now)
	default:
		return time.Time{}, fmt.Errorf("unsupported schedule mode %q", schedule.Mode)
	}
}

func parseClock(value string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("must use HH:MM")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("hour must be between 00 and 23")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("minute must be between 00 and 59")
	}
	return hour, minute, nil
}

func parseCron(expression string) (cronSpec, error) {
	parts := strings.Fields(expression)
	if len(parts) != 5 {
		return cronSpec{}, fmt.Errorf("must contain exactly 5 fields: minute hour day-of-month month day-of-week")
	}
	minute, err := parseCronField(parts[0], 0, 59, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("minute: %w", err)
	}
	hour, err := parseCronField(parts[1], 0, 23, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("hour: %w", err)
	}
	dom, err := parseCronField(parts[2], 1, 31, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("day-of-month: %w", err)
	}
	month, err := parseCronField(parts[3], 1, 12, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("month: %w", err)
	}
	dow, err := parseCronField(parts[4], 0, 7, true)
	if err != nil {
		return cronSpec{}, fmt.Errorf("day-of-week: %w", err)
	}
	return cronSpec{minute: minute, hour: hour, dom: dom, month: month, dow: dow}, nil
}

func parseCronField(input string, min, max int, sundayAlias bool) (cronField, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return cronField{}, fmt.Errorf("field is empty")
	}
	field := cronField{values: map[int]bool{}, wildcard: input == "*"}
	for _, component := range strings.Split(input, ",") {
		component = strings.TrimSpace(component)
		if component == "" {
			return cronField{}, fmt.Errorf("empty list item")
		}
		base, stepText, hasStep := strings.Cut(component, "/")
		step := 1
		if hasStep {
			if strings.Contains(stepText, "/") || stepText == "" {
				return cronField{}, fmt.Errorf("invalid step %q", component)
			}
			parsed, err := strconv.Atoi(stepText)
			if err != nil || parsed < 1 {
				return cronField{}, fmt.Errorf("step must be a positive integer")
			}
			step = parsed
		}
		start, end := min, max
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			left, right, ok := strings.Cut(base, "-")
			if !ok || strings.Contains(right, "-") {
				return cronField{}, fmt.Errorf("invalid range %q", base)
			}
			var err error
			start, err = parseCronNumber(left, min, max)
			if err != nil {
				return cronField{}, err
			}
			end, err = parseCronNumber(right, min, max)
			if err != nil {
				return cronField{}, err
			}
			if start > end {
				return cronField{}, fmt.Errorf("range start must not exceed end")
			}
		default:
			if hasStep {
				return cronField{}, fmt.Errorf("steps require * or a range")
			}
			value, err := parseCronNumber(base, min, max)
			if err != nil {
				return cronField{}, err
			}
			start, end = value, value
		}
		for value := start; value <= end; value += step {
			if sundayAlias && value == 7 {
				field.values[0] = true
			} else {
				field.values[value] = true
			}
		}
	}
	if len(field.values) == 0 {
		return cronField{}, fmt.Errorf("field selects no values")
	}
	return field, nil
}

func parseCronNumber(input string, min, max int) (int, error) {
	value, err := strconv.Atoi(input)
	if err != nil || value < min || value > max {
		return 0, fmt.Errorf("value %q must be between %d and %d", input, min, max)
	}
	return value, nil
}

func (spec cronSpec) matches(t time.Time) bool {
	if !spec.minute.values[t.Minute()] || !spec.hour.values[t.Hour()] || !spec.month.values[int(t.Month())] {
		return false
	}
	domMatch := spec.dom.values[t.Day()]
	dowMatch := spec.dow.values[int(t.Weekday())]
	dayMatch := domMatch && dowMatch
	if spec.dom.wildcard && spec.dow.wildcard {
		dayMatch = true
	} else if spec.dom.wildcard {
		dayMatch = dowMatch
	} else if spec.dow.wildcard {
		dayMatch = domMatch
	} else {
		// Traditional five-field cron treats restricted DOM and DOW as OR.
		dayMatch = domMatch || dowMatch
	}
	return dayMatch
}

func (spec cronSpec) next(after time.Time) (time.Time, error) {
	candidate := after.Truncate(time.Minute).Add(time.Minute)
	limit := candidate.AddDate(5, 0, 0)
	for !candidate.After(limit) {
		if spec.matches(candidate) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron expression has no matching time within five years")
}
