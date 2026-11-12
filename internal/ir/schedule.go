// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

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
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)
	rfc3339MinuteOffsetRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:00(?:Z|[+-]\d{2}:\d{2})$`)
	runtimeProfileNameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
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
	// AllowCalendar accepts the calendar and business_days keys.
	AllowCalendar bool
}

// maxBusinessDayOrdinal is the largest business-day position a month can
// have: 23 weekdays, with every weekday a business day.
const maxBusinessDayOrdinal = 23

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

// Location returns the time zone named by the schedule's TZ or CRON_TZ
// prefix, or nil when the schedule names none.
func (s Schedule) Location() *time.Location {
	if !s.IsCron() {
		return nil
	}
	parsed, ok := s.baseCron()
	if !ok {
		return nil
	}
	return cronLocation(parsed)
}

// cronLocation returns the zone a parsed cron expression names, or nil.
func cronLocation(parsed cron.Schedule) *time.Location {
	spec, ok := parsed.(*cron.SpecSchedule)
	if !ok || spec.Location == time.Local {
		return nil
	}
	return spec.Location
}

// HasCalendar reports whether the schedule fires on the business days of a
// calendar.
func (s Schedule) HasCalendar() bool {
	return s.Calendar != "" && s.Calendar != CalendarNone
}

// BindCalendar makes the schedule fire only on the business days of cal,
// the calendar the schedule names. Until it is bound, a calendar schedule
// never fires.
func (s *Schedule) BindCalendar(cal *Calendar) error {
	if !s.HasCalendar() {
		return fmt.Errorf("schedule names no calendar")
	}
	if cal == nil || cal.Name != s.Calendar {
		return fmt.Errorf("schedule names calendar %q", s.Calendar)
	}
	base, ok := s.baseCron()
	if !ok {
		return fmt.Errorf("calendar %q needs a cron schedule", s.Calendar)
	}
	s.Parsed = calendarSchedule{
		base:         base,
		calendar:     cal,
		businessDays: s.BusinessDays,
		location:     cronLocation(base),
	}
	return nil
}

// BoundCalendar returns the calendar bound to the schedule, if any.
func (s Schedule) BoundCalendar() (*Calendar, bool) {
	bound, ok := s.Parsed.(calendarSchedule)
	if !ok {
		return nil, false
	}
	return bound.calendar, true
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
	if normalized.HasCalendar() {
		profileSuffix += "|calendar:" + normalized.Calendar
	}
	if len(normalized.BusinessDays) > 0 {
		profileSuffix += "|business_days:" + formatBusinessDays(normalized.BusinessDays)
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
	schedule.Calendar = s.Calendar
	schedule.BusinessDays = s.BusinessDays
	if schedule.HasCalendar() {
		// A calendar schedule fires only once its calendar is bound.
		schedule.Parsed = nil
	}
	return schedule, nil
}

func parseScheduleMap(m map[string]any, opts ScheduleParseOptions) (Schedule, error) {
	var (
		kind         ScheduleKind
		expression   string
		at           string
		profile      string
		calendar     string
		businessDays []int
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
			if !runtimeProfileNameRe.MatchString(profile) {
				return Schedule{}, fmt.Errorf("invalid profile name: %q", profile)
			}
		case "calendar":
			val, ok := value.(string)
			if !ok {
				return Schedule{}, fmt.Errorf("calendar must be a string, got %T", value)
			}
			calendar = strings.TrimSpace(val)
			if err := ValidateCalendarName(calendar); err != nil {
				return Schedule{}, err
			}
		case "business_days":
			days, err := parseBusinessDays(value)
			if err != nil {
				return Schedule{}, err
			}
			businessDays = days
		default:
			return Schedule{}, fmt.Errorf("unknown key %q", key)
		}
	}

	if (calendar != "" || businessDays != nil) && !opts.AllowCalendar {
		return Schedule{}, fmt.Errorf("calendar and business_days are only supported for start schedules")
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
	if kind == ScheduleKindAt && (calendar != "" || businessDays != nil) {
		return Schedule{}, fmt.Errorf("one-off schedules do not take calendar or business_days")
	}

	schedule, err := normalizeSchedule(Schedule{
		Kind:         kind,
		Expression:   expression,
		At:           at,
		Profile:      profile,
		Calendar:     calendar,
		BusinessDays: businessDays,
	})
	if err != nil {
		return Schedule{}, err
	}
	if businessDays != nil && !matchesEveryDay(schedule.Expression) {
		return Schedule{}, fmt.Errorf("business_days requires * in the day-of-month and day-of-week fields of %q", schedule.Expression)
	}
	return schedule, nil
}

// parseBusinessDays decodes a business_days list from YAML or JSON.
func parseBusinessDays(value any) ([]int, error) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("business_days must be a non-empty list of integers")
	}
	days := make([]int, 0, len(items))
	seen := make(map[int]struct{}, len(items))
	for _, item := range items {
		day, ok := integerValue(item)
		if !ok || day == 0 || day > maxBusinessDayOrdinal || day < -maxBusinessDayOrdinal {
			return nil, fmt.Errorf("business_days entries must be integers from 1 to %d, or -%d to -1, got %v",
				maxBusinessDayOrdinal, maxBusinessDayOrdinal, item)
		}
		if _, dup := seen[day]; dup {
			return nil, fmt.Errorf("business_days lists %d twice", day)
		}
		seen[day] = struct{}{}
		days = append(days, day)
	}
	return days, nil
}

func integerValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case uint64:
		if v > maxBusinessDayOrdinal {
			return 0, false
		}
		return int(v), true
	case float64:
		return int(v), v == float64(int(v))
	default:
		return 0, false
	}
}

func formatBusinessDays(days []int) string {
	parts := make([]string, len(days))
	for i, day := range days {
		parts[i] = strconv.Itoa(day)
	}
	return strings.Join(parts, ",")
}

// matchesEveryDay reports whether a cron expression leaves both day fields
// unrestricted, so that business_days alone chooses the days.
func matchesEveryDay(expr string) bool {
	_, body := splitTimezonePrefix(expr)
	fields := strings.Fields(body)
	return len(fields) == 5 && fields[2] == "*" && fields[4] == "*"
}

func parseCronExpression(expr string) (cron.Schedule, string, error) {
	normalized := strings.Join(strings.Fields(expr), " ")
	if normalized == "" {
		return nil, "", fmt.Errorf("cron expression must not be empty")
	}

	// "@every <duration>" has no calendar-time equivalent, so it keeps its
	// descriptor form, with the shortest duration, as the canonical expression.
	tzPrefix, body := splitTimezonePrefix(normalized)
	if duration, ok := strings.CutPrefix(body, "@every "); ok {
		// Interval boundaries are aligned to the Unix epoch in every zone, so
		// a timezone prefix could not be honored.
		if tzPrefix != "" {
			return nil, "", fmt.Errorf("invalid cron expression %q: @every does not accept a timezone prefix", normalized)
		}
		parsed, err := newIntervalSchedule(duration)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cron expression %q: %w", normalized, err)
		}
		return parsed, "@every " + formatInterval(parsed.delay), nil
	}

	// Canonical expressions keep equivalent schedules identical in durable state.
	switch body {
	case "@hourly":
		normalized = tzPrefix + "0 * * * *"
	case "@daily", "@midnight":
		normalized = tzPrefix + "0 0 * * *"
	case "@weekly":
		normalized = tzPrefix + "0 0 * * 0"
	case "@monthly":
		normalized = tzPrefix + "0 0 1 * *"
	case "@yearly", "@annually":
		normalized = tzPrefix + "0 0 1 1 *"
	}

	parsed, err := standardCronParser.Parse(normalized)
	if err != nil {
		return nil, "", fmt.Errorf("invalid cron expression %q: %w", normalized, err)
	}
	return parsed, normalized, nil
}

// splitTimezonePrefix separates a leading "TZ=" or "CRON_TZ=" field from the
// rest of a whitespace-normalized cron expression. The prefix keeps its
// trailing space so that prefix+body reproduces expr.
func splitTimezonePrefix(expr string) (prefix, body string) {
	if !strings.HasPrefix(expr, "TZ=") && !strings.HasPrefix(expr, "CRON_TZ=") {
		return "", expr
	}
	i := strings.IndexByte(expr, ' ')
	if i < 0 {
		return "", expr
	}
	return expr[:i+1], expr[i+1:]
}

// intervalSchedule fires once every delay on a fixed grid aligned to the Unix
// epoch. "@every <duration>" cannot use robfig's ConstantDelaySchedule, which
// is relative to the evaluation instant; the scheduler asks which absolute
// fire time falls inside the current tick, so a relative delay would never
// be due.
type intervalSchedule struct {
	delay time.Duration // positive whole minutes
}

func newIntervalSchedule(arg string) (intervalSchedule, error) {
	delay, err := time.ParseDuration(arg)
	if err != nil {
		return intervalSchedule{}, err
	}
	// The scheduler evaluates schedules on whole-minute ticks, so any other
	// interval would fire on a coarser grid than configured.
	if delay < time.Minute || delay%time.Minute != 0 {
		return intervalSchedule{}, fmt.Errorf("interval must be a positive whole number of minutes")
	}
	return intervalSchedule{delay: delay}, nil
}

// formatInterval returns the shortest duration string for a whole-minute
// interval, e.g. "1h" rather than "1h0m0s" or "60m".
func formatInterval(d time.Duration) string {
	s := strings.TrimSuffix(d.String(), "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Next returns the next interval boundary strictly after t.
func (s intervalSchedule) Next(t time.Time) time.Time {
	delaySec := int64(s.delay / time.Second)
	next := (t.Unix()/delaySec + 1) * delaySec
	return time.Unix(next, 0).In(t.Location())
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

// parsedCron returns the schedule that decides fire times. A calendar
// schedule has none until its calendar is bound, so decoded copies of it,
// such as index entries and run snapshots, never fire on a holiday.
func (s Schedule) parsedCron() (cron.Schedule, bool) {
	if s.Parsed != nil {
		return s.Parsed, true
	}
	if s.HasCalendar() {
		return nil, false
	}
	return s.baseCron()
}

// baseCron returns the cron schedule without its calendar.
func (s Schedule) baseCron() (cron.Schedule, bool) {
	switch parsed := s.Parsed.(type) {
	case calendarSchedule:
		return parsed.base, true
	case nil:
	default:
		return parsed, true
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
