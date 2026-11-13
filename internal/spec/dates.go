// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// dateSpecKeys are the keys of an entry of the dates block in object form.
var dateSpecKeys = []string{"from", "each", "shift", "format", "timezone"}

// buildDates decodes the dates block into entries in document order. Their
// meaning is checked by validateDates once the schedule is known.
func buildDates(_ buildContext, d *dag) ([]ir.DateSpec, error) {
	if d.Dates == nil {
		return nil, nil
	}
	var specs []ir.DateSpec
	switch raw := d.Dates.(type) {
	case []any:
		// The loader's ordered form of the mapping.
		for _, item := range raw {
			entry, ok := item.(map[string]any)
			if !ok || len(entry) != 1 {
				return nil, &FieldError{Path: "dates", Err: errors.New("dates must be a mapping of names to dates")}
			}
			for name, value := range entry {
				spec, err := decodeDateSpec(name, value)
				if err != nil {
					return nil, err
				}
				specs = append(specs, spec)
			}
		}
	case map[string]any:
		names := make([]string, 0, len(raw))
		for name := range raw {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			spec, err := decodeDateSpec(name, raw[name])
			if err != nil {
				return nil, err
			}
			specs = append(specs, spec)
		}
	default:
		return nil, &FieldError{Path: "dates", Err: errors.New("dates must be a mapping of names to dates")}
	}
	return specs, nil
}

// decodeDateSpec decodes one entry: a shift written as a string or a list,
// or an object.
func decodeDateSpec(name string, value any) (ir.DateSpec, error) {
	path := "dates." + name
	spec := ir.DateSpec{Name: name}
	switch v := value.(type) {
	case string, []any:
		shift, err := decodeDateShift(v)
		if err != nil {
			return ir.DateSpec{}, &FieldError{Path: path, Err: err}
		}
		spec.Shift = shift
		return spec, nil
	case map[string]any:
		for key := range v {
			if !slices.Contains(dateSpecKeys, key) {
				return ir.DateSpec{}, &FieldError{Path: path + "." + key,
					Err: fmt.Errorf("unknown key %q: use %s", key, strings.Join(dateSpecKeys, ", "))}
			}
		}
		var err error
		fields := map[string]*string{
			"from":     &spec.From,
			"each":     &spec.Each,
			"format":   &spec.Format,
			"timezone": &spec.Timezone,
		}
		for key, target := range fields {
			raw, ok := v[key]
			if !ok {
				continue
			}
			text, isString := raw.(string)
			if !isString || strings.TrimSpace(text) == "" {
				return ir.DateSpec{}, &FieldError{Path: path + "." + key, Err: errors.New("must be a non-empty string")}
			}
			if key == "format" {
				// A strftime pattern keeps its spaces and newlines.
				*target = text
				continue
			}
			*target = strings.TrimSpace(text)
		}
		if raw, ok := v["shift"]; ok {
			if spec.Shift, err = decodeDateShift(raw); err != nil {
				return ir.DateSpec{}, &FieldError{Path: path + ".shift", Err: err}
			}
		}
		return spec, nil
	default:
		return ir.DateSpec{}, &FieldError{Path: path,
			Err: errors.New("a date is a shift such as -1d, a list of shifts, or an object with from, each, shift, format, and timezone")}
	}
}

// decodeDateShift decodes a shift: one operation or a list of them.
func decodeDateShift(value any) ([]string, error) {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, errors.New("shift must not be empty")
		}
		return []string{strings.TrimSpace(v)}, nil
	case []any:
		ops := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, errors.New("each shift operation must be a non-empty string, such as -1d")
			}
			ops = append(ops, strings.TrimSpace(text))
		}
		return ops, nil
	default:
		return nil, errors.New("shift must be an operation such as -1d, or a list of operations")
	}
}

// validateDates checks the dates block against the DAG it belongs to.
// Business days need a calendar for every slot the DAG's schedule gives,
// or the DAG's own calendar when it has no recurring start schedule. A date
// that needs a schedule window is only a warning on a DAG without one, since
// a sub-DAG run takes its parent's window.
func (s *dagBuildState) validateDates() {
	dates := s.result.Dates
	if len(dates) == 0 {
		return
	}
	var invalid bool
	objects := objectFormDates(s.spec.Dates)
	for _, err := range ir.ValidateDates(dates) {
		invalid = true
		s.errs = append(s.errs, dateFieldError(err, objects))
	}
	// A base config's schedules take their calendar from the DAG it merges
	// into, so the DAG checks them.
	if invalid || s.ctx.opts.Has(buildFlagBaseDefinition) {
		return
	}

	recurring := slices.DeleteFunc(slices.Clone(s.result.Schedule), func(sched ir.Schedule) bool {
		return !sched.IsCron()
	})
	if names := ir.DatesUsingBusinessDays(dates); len(names) > 0 {
		if err := businessDayCalendarError(recurring, s.result.Calendar); err != nil {
			s.errs = append(s.errs, &FieldError{Path: "dates." + names[0],
				Err: fmt.Errorf("%s uses business days, and %w", strings.Join(names, ", "), err)})
		}
	}
	if len(recurring) == 0 {
		for _, name := range ir.DatesNeedingWindow(dates) {
			s.datesWarnings = append(s.datesWarnings, fmt.Sprintf(
				"dates.%s needs a schedule window, and the DAG has no recurring start schedule: "+
					"only a sub-DAG run of a parent with a schedule slot can resolve it", name))
		}
	}
}

// dateFieldError places a dates error at the field at fault, or at the
// entry when it is written as a shift rather than an object.
func dateFieldError(err error, objects map[string]bool) error {
	dateErr, ok := errors.AsType[*ir.DateError](err)
	if !ok {
		return &FieldError{Path: "dates", Err: err}
	}
	path := "dates." + dateErr.Name
	switch {
	case dateErr.Field == "":
		return &FieldError{Path: path, Err: dateErr.Err}
	case objects[dateErr.Name]:
		return &FieldError{Path: path + "." + dateErr.Field, Err: dateErr.Err}
	default:
		return &FieldError{Path: path, Err: fmt.Errorf("%s: %w", dateErr.Field, dateErr.Err)}
	}
}

// objectFormDates returns the names of the entries written as objects.
func objectFormDates(raw any) map[string]bool {
	objects := make(map[string]bool)
	add := func(name string, value any) {
		if _, ok := value.(map[string]any); ok {
			objects[name] = true
		}
	}
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			if entry, ok := item.(map[string]any); ok {
				for name, value := range entry {
					add(name, value)
				}
			}
		}
	case map[string]any:
		for name, value := range v {
			add(name, value)
		}
	}
	return objects
}

// businessDayCalendarError reports why some run of the DAG would have no
// calendar for business days.
func businessDayCalendarError(recurring []ir.Schedule, dagCalendar string) error {
	if len(recurring) == 0 {
		if dagCalendar == "" || dagCalendar == ir.CalendarNone {
			return errors.New("the DAG has neither a recurring start schedule nor a calendar")
		}
		return nil
	}
	for _, sched := range recurring {
		if !sched.HasCalendar() {
			return fmt.Errorf("schedule %q has no calendar", sched.Expression)
		}
	}
	return nil
}
