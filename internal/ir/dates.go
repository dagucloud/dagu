// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DateSpec is one entry of a DAG's dates block: a date derived from the
// run's schedule slot.
type DateSpec struct {
	// Name is the entry's name, read as ${dates.NAME}.
	Name string `json:"name"`
	// From is the instant the entry starts from: a DateFrom* base or the name
	// of another entry. Empty means scheduled_at.
	From string `json:"from,omitempty"`
	// Each steps through the run's window, making the entry a list, such as
	// "1d". Empty for a single date.
	Each string `json:"each,omitempty"`
	// Shift lists the operations applied, left to right, such as "-1d" or
	// "month_start".
	Shift []string `json:"shift,omitempty"`
	// Format is a named format or a strftime pattern. Empty means "date".
	Format string `json:"format,omitempty"`
	// Timezone is the zone the entry is shifted and formatted in. Empty
	// means the zone of the entry it builds on, else the run's.
	Timezone string `json:"timezone,omitempty"`
}

// The instants an entry of the dates block can start from, besides another
// entry.
const (
	DateFromScheduledAt     = "scheduled_at"
	DateFromIntervalStart   = "interval_start"
	DateFromIntervalEnd     = "interval_end"
	DateFromNextScheduledAt = "next_scheduled_at"
)

// The named formats of the dates block.
const (
	DateFormatDate        = "date"
	DateFormatDateCompact = "date_compact"
	DateFormatDatetime    = "datetime"
	DateFormatRFC3339     = "rfc3339"
	DateFormatUnix        = "unix"
)

// MaxDateListValues bounds the values of an entry that steps through the
// window, as the parallel step bounds its items.
const MaxDateListValues = 1000

// maxDateAmount bounds the amount of a shift or a step.
const maxDateAmount = 10000

var dateBases = []string{
	DateFromScheduledAt,
	DateFromIntervalStart,
	DateFromIntervalEnd,
	DateFromNextScheduledAt,
}

var dateNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// dateUnit is the unit of a shift or a step.
type dateUnit string

const (
	unitMinute      dateUnit = "min"
	unitHour        dateUnit = "h"
	unitDay         dateUnit = "d"
	unitWeek        dateUnit = "w"
	unitMonth       dateUnit = "mo"
	unitYear        dateUnit = "y"
	unitBusinessDay dateUnit = "bd"
)

// dateUnits lists the units in the order error messages name them.
var dateUnits = []dateUnit{unitMinute, unitHour, unitDay, unitWeek, unitMonth, unitYear, unitBusinessDay}

// dateOpKind is what a shift operation does.
type dateOpKind int

const (
	opAdd dateOpKind = iota
	opStartOf
	opBusinessDayOrNext
	opBusinessDayOrPrev
)

// dateOp is a parsed shift operation.
type dateOp struct {
	kind dateOpKind
	// amount is the signed amount of an opAdd.
	amount int
	// unit is the unit of an opAdd, or the unit an opStartOf truncates to.
	unit dateUnit
}

// startOfOps maps the start-of operations to the unit they truncate to.
var startOfOps = map[string]dateUnit{
	"hour_start":  unitHour,
	"day_start":   unitDay,
	"week_start":  unitWeek,
	"month_start": unitMonth,
	"year_start":  unitYear,
}

const (
	opNameBusinessDayOrNext = "bd_or_next"
	opNameBusinessDayOrPrev = "bd_or_prev"
)

var amountPattern = regexp.MustCompile(`^([+-]?)([0-9]+)([a-z]*)$`)

// usesBusinessDays reports whether the operation reads a business calendar.
func (op dateOp) usesBusinessDays() bool {
	return op.unit == unitBusinessDay || op.kind == opBusinessDayOrNext || op.kind == opBusinessDayOrPrev
}

// parseDateOp parses one shift operation, such as "-1d" or "month_start".
func parseDateOp(text string) (dateOp, error) {
	switch text {
	case opNameBusinessDayOrNext:
		return dateOp{kind: opBusinessDayOrNext}, nil
	case opNameBusinessDayOrPrev:
		return dateOp{kind: opBusinessDayOrPrev}, nil
	}
	if unit, ok := startOfOps[text]; ok {
		return dateOp{kind: opStartOf, unit: unit}, nil
	}
	parsed, err := parseAmount(text)
	if err != nil {
		return dateOp{}, err
	}
	amount := parsed.amount
	switch parsed.sign {
	case "":
		return dateOp{}, fmt.Errorf("shift %q needs a sign, such as +%s or -%s", text, text, text)
	case "-":
		amount = -amount
	}
	return dateOp{kind: opAdd, amount: amount, unit: parsed.unit}, nil
}

// parseDateStep parses the step of an entry that steps through the window,
// such as "1d".
func parseDateStep(text string) (int, dateUnit, error) {
	parsed, err := parseAmount(text)
	if err != nil {
		return 0, "", err
	}
	if parsed.sign != "" {
		return 0, "", fmt.Errorf("step %q takes no sign: use %s", text, strings.TrimLeft(text, "+-"))
	}
	if parsed.amount == 0 {
		return 0, "", fmt.Errorf("step %q must be at least 1", text)
	}
	return parsed.amount, parsed.unit, nil
}

// dateAmount is an amount with a unit, such as "-1d", split into its parts.
type dateAmount struct {
	// sign is "+", "-", or empty.
	sign   string
	amount int
	unit   dateUnit
}

func parseAmount(text string) (dateAmount, error) {
	m := amountPattern.FindStringSubmatch(text)
	if m == nil {
		return dateAmount{}, fmt.Errorf("unknown operation %q: use +N or -N with %s, or one of %s",
			text, unitList(), strings.Join(namedOps(), ", "))
	}
	unit := dateUnit(m[3])
	switch {
	case unit == "m":
		return dateAmount{}, fmt.Errorf("unknown unit %q in %q: use min for minutes or mo for months", unit, text)
	case !slices.Contains(dateUnits, unit):
		return dateAmount{}, fmt.Errorf("unknown unit %q in %q: use %s", unit, text, unitList())
	}
	amount, err := strconv.Atoi(m[2])
	if err != nil || amount > maxDateAmount {
		return dateAmount{}, fmt.Errorf("amount in %q must be at most %d", text, maxDateAmount)
	}
	return dateAmount{sign: m[1], amount: amount, unit: unit}, nil
}

func unitList() string {
	names := make([]string, len(dateUnits))
	for i, unit := range dateUnits {
		names[i] = string(unit)
	}
	return strings.Join(names, ", ")
}

func namedOps() []string {
	names := []string{"hour_start", "day_start", "week_start", "month_start", "year_start"}
	return append(names, opNameBusinessDayOrNext, opNameBusinessDayOrPrev)
}

// isDateBase reports whether from names an instant of the run rather than
// another entry.
func isDateBase(from string) bool {
	return slices.Contains(dateBases, from)
}

// fromOf returns the instant or entry spec starts from.
func fromOf(spec DateSpec) string {
	if spec.From == "" {
		return DateFromScheduledAt
	}
	return spec.From
}

// DateError is a problem with one entry of a dates block.
type DateError struct {
	// Name is the entry's name.
	Name string
	// Field is the entry's field at fault, such as "shift", or empty for
	// the entry as a whole.
	Field string
	Err   error
}

func (e *DateError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("dates.%s: %v", e.Name, e.Err)
	}
	return fmt.Sprintf("dates.%s: %s: %v", e.Name, e.Field, e.Err)
}

func (e *DateError) Unwrap() error {
	return e.Err
}

// ValidateDates checks a dates block on its own: names, references between
// entries, operations, steps, formats, and zones. It returns every problem
// found, each a *DateError.
func ValidateDates(specs []DateSpec) []error {
	var errs []error
	byName := make(map[string]DateSpec, len(specs))
	for _, spec := range specs {
		switch {
		case !dateNamePattern.MatchString(spec.Name):
			errs = append(errs, &DateError{Name: spec.Name,
				Err: errors.New("invalid name: use letters, digits, and '_', starting with a letter")})
			continue
		case isDateBase(spec.Name):
			errs = append(errs, &DateError{Name: spec.Name,
				Err: errors.New("the name is reserved for an instant of the run")})
			continue
		}
		if _, dup := byName[spec.Name]; dup {
			errs = append(errs, &DateError{Name: spec.Name, Err: errors.New("defined more than once")})
			continue
		}
		byName[spec.Name] = spec
	}
	for _, spec := range specs {
		if _, ok := byName[spec.Name]; !ok {
			continue
		}
		for _, err := range validateDateSpec(spec, byName) {
			err.Name = spec.Name
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		if cycle := dateCycle(specs, byName); cycle != nil {
			errs = append(errs, &DateError{Name: cycle[0],
				Err: fmt.Errorf("entries build on each other in a cycle: %s", strings.Join(cycle, " -> "))})
		}
	}
	return errs
}

func validateDateSpec(spec DateSpec, byName map[string]DateSpec) []*DateError {
	var errs []*DateError
	fail := func(field string, err error) {
		errs = append(errs, &DateError{Field: field, Err: err})
	}
	if spec.Each != "" {
		if spec.From != "" {
			fail("from", errors.New("cannot be combined with each: a list steps from interval_start"))
		}
		_, unit, err := parseDateStep(spec.Each)
		switch {
		case err != nil:
			fail("each", err)
		case spec.Format == "" && (unit == unitMinute || unit == unitHour):
			fail("each", fmt.Errorf("a step of %q repeats dates in the default format: set format", spec.Each))
		}
	} else if from := fromOf(spec); !isDateBase(from) {
		switch target, ok := byName[from]; {
		case !ok:
			fail("from", fmt.Errorf("unknown %q: use %s, or another entry", from, strings.Join(dateBases, ", ")))
		case target.Each != "":
			fail("from", fmt.Errorf("%q is a list", from))
		}
	}
	for _, text := range spec.Shift {
		if _, err := parseDateOp(text); err != nil {
			fail("shift", err)
		}
	}
	if err := validateDateFormat(spec.Format); err != nil {
		fail("format", err)
	}
	if spec.Timezone != "" {
		if _, err := time.LoadLocation(spec.Timezone); err != nil {
			fail("timezone", fmt.Errorf("unknown zone %q", spec.Timezone))
		}
	}
	return errs
}

// dateCycle returns the names of entries that build on each other in a
// cycle, starting and ending with the same name, or nil when there is none.
// The entries are valid otherwise.
func dateCycle(specs []DateSpec, byName map[string]DateSpec) []string {
	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(specs))
	var path []string
	var visit func(name string) []string
	visit = func(name string) []string {
		switch state[name] {
		case visiting:
			start := slices.Index(path, name)
			return append(slices.Clone(path[start:]), name)
		case done:
			return nil
		}
		state[name] = visiting
		path = append(path, name)
		spec := byName[name]
		if from := fromOf(spec); spec.Each == "" && !isDateBase(from) {
			if cycle := visit(from); cycle != nil {
				return cycle
			}
		}
		path = path[:len(path)-1]
		state[name] = done
		return nil
	}
	for _, spec := range specs {
		if cycle := visit(spec.Name); cycle != nil {
			return cycle
		}
	}
	return nil
}

// DatesUsingBusinessDays returns the names of entries that read a business
// calendar, through a business-day shift or step, in declaration order.
// Entries that do not parse are left out.
func DatesUsingBusinessDays(specs []DateSpec) []string {
	var names []string
	for _, spec := range specs {
		if dateSpecUsesBusinessDays(spec) {
			names = append(names, spec.Name)
		}
	}
	return names
}

func dateSpecUsesBusinessDays(spec DateSpec) bool {
	if spec.Each != "" {
		if _, unit, err := parseDateStep(spec.Each); err == nil && unit == unitBusinessDay {
			return true
		}
	}
	for _, text := range spec.Shift {
		if op, err := parseDateOp(text); err == nil && op.usesBusinessDays() {
			return true
		}
	}
	return false
}

// DatesNeedingWindow returns the names of entries that need the run's
// schedule window: those that step through it, start from interval_start or
// next_scheduled_at, or build on such an entry. The entries must be valid.
func DatesNeedingWindow(specs []DateSpec) []string {
	byName := make(map[string]DateSpec, len(specs))
	for _, spec := range specs {
		byName[spec.Name] = spec
	}
	var names []string
	for _, spec := range specs {
		if dateNeedsWindow(spec, byName, len(specs)) {
			names = append(names, spec.Name)
		}
	}
	return names
}

// dateNeedsWindow follows the chain of entries spec builds on. depth bounds
// the walk, so a cycle ends it.
func dateNeedsWindow(spec DateSpec, byName map[string]DateSpec, depth int) bool {
	for range depth + 1 {
		if spec.Each != "" {
			return true
		}
		switch from := fromOf(spec); from {
		case DateFromIntervalStart, DateFromNextScheduledAt:
			return true
		case DateFromScheduledAt, DateFromIntervalEnd:
			return false
		default:
			next, ok := byName[from]
			if !ok {
				return false
			}
			spec = next
		}
	}
	return false
}
