// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var (
	standardCronParser = cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	rfc3339MinuteOffsetRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:00(?:Z|[+-]\d{2}:\d{2})$`)
)

const canonicalOneOffLayout = "2006-01-02T15:04:00Z07:00"

// ScheduleKind identifies the supported schedule types.
type ScheduleKind string

const (
	ScheduleKindCron ScheduleKind = "cron"
	ScheduleKindAt   ScheduleKind = "at"
)

// ScheduleParseOptions controls which schedule kinds are accepted.
type ScheduleParseOptions struct {
	AllowAt bool
}

// NewCronSchedule parses a cron schedule into its canonical representation.
func NewCronSchedule(expr string) (Schedule, error) {
	parsed, normalized, err := parseCronExpression(expr)
	if err != nil {
		return Schedule{}, err
	}
	warnings := checkMisleadingStepValues(normalized)
	return Schedule{
		Kind:       ScheduleKindCron,
		Expression: normalized,
		Parsed:     parsed,
		Warnings:   warnings,
	}, nil
}

// NewOneOffSchedule parses a one-off timestamp into its canonical representation.
func NewOneOffSchedule(at string) (Schedule, error) {
	atTime, normalized, err := parseOneOffTime(at)
	if err != nil {
		return Schedule{}, err
	}
	return Schedule{
		Kind:   ScheduleKindAt,
		At:     normalized,
		AtTime: atTime,
	}, nil
}

// ParseScheduleValue parses a YAML/JSON schedule entry into the canonical model.
func ParseScheduleValue(v any, opts ScheduleParseOptions) (Schedule, error) {
	switch val := v.(type) {
	case string:
		return NewCronSchedule(val)
	case map[string]any:
		return parseScheduleMap(val, opts)
	case map[any]any:
		converted := make(map[string]any, len(val))
		for key, item := range val {
			keyStr, ok := key.(string)
			if !ok {
				return Schedule{}, fmt.Errorf("expected string key, got %T", key)
			}
			converted[keyStr] = item
		}
		return parseScheduleMap(converted, opts)
	default:
		return Schedule{}, fmt.Errorf("expected string or object, got %T", v)
	}
}

// GetKind returns the normalized schedule kind.
func (s Schedule) GetKind() ScheduleKind {
	switch {
	case s.Kind != "":
		return s.Kind
	case s.At != "" || !s.AtTime.IsZero():
		return ScheduleKindAt
	case s.Expression != "" || s.Parsed != nil:
		return ScheduleKindCron
	default:
		return ""
	}
}

// IsCron reports whether the schedule is cron-based.
func (s Schedule) IsCron() bool {
	return s.GetKind() == ScheduleKindCron
}

// IsOneOff reports whether the schedule is a one-off timestamp.
func (s Schedule) IsOneOff() bool {
	return s.GetKind() == ScheduleKindAt
}

// Next returns the next metadata-derived run time for this schedule.
func (s Schedule) Next(now time.Time) time.Time {
	switch s.GetKind() {
	case ScheduleKindCron:
		parsed, ok := s.parsedCron()
		if !ok {
			return time.Time{}
		}
		return parsed.Next(now)
	case ScheduleKindAt:
		at, ok := s.oneOffTime()
		if !ok || !at.After(now) {
			return time.Time{}
		}
		return at
	default:
		return time.Time{}
	}
}

// Fingerprint returns the canonical schedule fingerprint used for durable state.
func (s Schedule) Fingerprint() string {
	normalized, err := s.normalized()
	if err != nil {
		return ""
	}
	profileSuffix := ""
	if normalized.Profile != "" {
		profileSuffix = "|profile:" + normalized.Profile
	}
	switch normalized.Kind {
	case ScheduleKindCron:
		return "cron:" + normalized.Expression + profileSuffix
	case ScheduleKindAt:
		return "at:" + normalized.At + profileSuffix
	default:
		return ""
	}
}

// DisplayValue returns the user-facing schedule value.
func (s Schedule) DisplayValue() string {
	switch s.GetKind() {
	case ScheduleKindCron:
		return s.Expression
	case ScheduleKindAt:
		return s.At
	default:
		return ""
	}
}

// OneOffTime returns the parsed one-off schedule time, if any.
func (s Schedule) OneOffTime() (time.Time, bool) {
	return s.oneOffTime()
}

func (s Schedule) normalized() (Schedule, error) {
	return normalizeSchedule(s)
}

func normalizeSchedule(s Schedule) (Schedule, error) {
	profile := strings.TrimSpace(s.Profile)
	var schedule Schedule
	var err error
	switch s.GetKind() {
	case ScheduleKindCron:
		schedule, err = NewCronSchedule(s.Expression)
	case ScheduleKindAt:
		schedule, err = NewOneOffSchedule(s.At)
	case "":
		return Schedule{}, nil
	default:
		return Schedule{}, fmt.Errorf("unsupported schedule kind %q", s.Kind)
	}
	if err != nil {
		return Schedule{}, err
	}
	schedule.Profile = profile
	return schedule, nil
}

func parseScheduleMap(m map[string]any, opts ScheduleParseOptions) (Schedule, error) {
	var (
		kind       ScheduleKind
		expression string
		at         string
		profile    string
	)

	for key, value := range m {
		switch key {
		case "kind":
			val, ok := value.(string)
			if !ok {
				return Schedule{}, fmt.Errorf("kind must be a string, got %T", value)
			}
			kind = ScheduleKind(strings.TrimSpace(val))
		case "expression":
			val, ok := value.(string)
			if !ok {
				return Schedule{}, fmt.Errorf("expression must be a string, got %T", value)
			}
			expression = val
		case "at":
			val, ok := value.(string)
			if !ok {
				return Schedule{}, fmt.Errorf("at must be a string, got %T", value)
			}
			at = val
		case "profile":
			val, ok := value.(string)
			if !ok {
				return Schedule{}, fmt.Errorf("profile must be a string, got %T", value)
			}
			profile = strings.TrimSpace(val)
		default:
			return Schedule{}, fmt.Errorf("unknown key %q", key)
		}
	}

	if expression != "" && at != "" {
		return Schedule{}, fmt.Errorf("schedule object must not include both expression and at")
	}

	if kind == "" {
		switch {
		case at != "":
			kind = ScheduleKindAt
		case expression != "":
			kind = ScheduleKindCron
		default:
			return Schedule{}, fmt.Errorf("schedule object must include either expression or at")
		}
	}

	switch kind {
	case ScheduleKindCron:
		if expression == "" {
			return Schedule{}, fmt.Errorf("cron schedules must include expression")
		}
		if at != "" {
			return Schedule{}, fmt.Errorf("cron schedules must not include at")
		}
	case ScheduleKindAt:
		if at == "" {
			return Schedule{}, fmt.Errorf("one-off schedules must include at")
		}
		if expression != "" {
			return Schedule{}, fmt.Errorf("one-off schedules must not include expression")
		}
	}

	if kind == ScheduleKindAt && !opts.AllowAt {
		return Schedule{}, fmt.Errorf("one-off schedules are only supported for start schedules")
	}

	return normalizeSchedule(Schedule{
		Kind:       kind,
		Expression: expression,
		At:         at,
		Profile:    profile,
	})
}

func parseCronExpression(expr string) (cron.Schedule, string, error) {
	normalized := strings.Join(strings.Fields(expr), " ")
	if normalized == "" {
		return nil, "", fmt.Errorf("cron expression must not be empty")
	}

	parsed, err := standardCronParser.Parse(normalized)
	if err != nil {
		return nil, "", fmt.Errorf("invalid cron expression %q: %w", normalized, err)
	}
	return parsed, normalized, nil
}

func checkMisleadingStepValues(expr string) []string {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil
	}

	type checkRule struct {
		fieldIndex int
		fieldName  string
		base       int
		unit       string
	}

	rules := []checkRule{
		{fieldIndex: 0, fieldName: "minute", base: 60, unit: "minutes"},
		{fieldIndex: 1, fieldName: "hour", base: 24, unit: "hours"},
	}

	warnings := make([]string, 0, len(rules))
	for _, rule := range rules {
		field := fields[rule.fieldIndex]
		if !strings.HasPrefix(field, "*/") {
			continue
		}

		step, err := strconv.Atoi(strings.TrimPrefix(field, "*/"))
		if err != nil || step <= 1 {
			continue
		}
		if isExpectedStepValue(rule.fieldName, rule.base, step) {
			continue
		}

		firesAt := make([]string, 0, rule.base/step+1)
		for i := 0; i < rule.base; i += step {
			firesAt = append(firesAt, strconv.Itoa(i))
		}

		divisors := make([]string, 0, rule.base)
		for i := 2; i < rule.base; i++ {
			if rule.base%i == 0 {
				divisors = append(divisors, strconv.Itoa(i))
			}
		}

		warnings = append(warnings, fmt.Sprintf(
			"schedule %q: %s in %s field fires at %s %s, not every %d %s. Divisors of %d that work as expected: %s",
			expr,
			field,
			rule.fieldName,
			strings.Join(firesAt, ", "),
			rule.unit,
			step,
			rule.unit,
			rule.base,
			strings.Join(divisors, ", "),
		))
	}

	return warnings
}

func isExpectedStepValue(fieldName string, base, step int) bool {
	if base%step == 0 {
		return true
	}

	// Common pattern: "*/5" in the hour field is often intentional, even
	// though the final interval in each day is shorter at the day boundary.
	if fieldName == "hour" && step == 5 {
		return true
	}

	return false
}

func parseOneOffTime(raw string) (time.Time, string, error) {
	if !rfc3339MinuteOffsetRe.MatchString(raw) {
		return time.Time{}, "", fmt.Errorf("one-off schedule %q must be RFC 3339 with an explicit offset and minute precision", raw)
	}

	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid one-off schedule %q: %w", raw, err)
	}

	if at.Second() != 0 || at.Nanosecond() != 0 {
		return time.Time{}, "", fmt.Errorf("one-off schedule %q must use minute precision", raw)
	}

	return at, at.Format(canonicalOneOffLayout), nil
}

func (s Schedule) parsedCron() (cron.Schedule, bool) {
	if s.Parsed != nil {
		return s.Parsed, true
	}
	if !s.IsCron() || s.Expression == "" {
		return nil, false
	}
	parsed, _, err := parseCronExpression(s.Expression)
	if err != nil {
		return nil, false
	}
	return parsed, true
}

func (s Schedule) oneOffTime() (time.Time, bool) {
	if !s.AtTime.IsZero() {
		return s.AtTime, true
	}
	if !s.IsOneOff() || s.At == "" {
		return time.Time{}, false
	}
	at, _, err := parseOneOffTime(s.At)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}
