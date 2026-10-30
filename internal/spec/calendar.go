// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/goccy/go-yaml"
)

const (
	// calendarFileExtension is the extension of calendar files.
	calendarFileExtension = ".yaml"
	// maxHolidayRangeDays bounds a holiday range, which is expanded day by
	// day.
	maxHolidayRangeDays = 366
	// CalendarExpiryWarningDays is how long before a calendar's through date
	// the DAGs that use it warn that its holidays run out.
	CalendarExpiryWarningDays = 30
)

// ErrUnknownCalendar reports a calendar name with no built-in calendar and
// no calendar file.
var ErrUnknownCalendar = errors.New("unknown calendar")

var weekdayNames = map[string]time.Weekday{
	"SUN": time.Sunday, "MON": time.Monday, "TUE": time.Tuesday, "WED": time.Wednesday,
	"THU": time.Thursday, "FRI": time.Friday, "SAT": time.Saturday,
}

// CalendarSummary describes a calendar for listings.
type CalendarSummary struct {
	Name        string
	Description string
	// Through is the last date the calendar's holidays are maintained for;
	// zero when the calendar does not say.
	Through ir.CivilDate
	Builtin bool
	// Err is set when the calendar file does not load.
	Err error
}

// LoadCalendar returns the named calendar: a built-in one, or the calendar
// file <name>.yaml in dir with the holidays of the calendars it includes.
func LoadCalendar(dir, name string) (*ir.Calendar, error) {
	if cal, ok := ir.BuiltinCalendar(name); ok {
		return cal, nil
	}
	loader := calendarLoader{dir: dir, loading: map[string]bool{}}
	return loader.load(name)
}

// ListCalendars lists the built-in calendars and the calendar files in dir,
// sorted by name. A missing dir lists only the built-in calendars.
func ListCalendars(dir string) ([]CalendarSummary, error) {
	weekdays, _ := ir.BuiltinCalendar(ir.CalendarWeekdays)
	summaries := []CalendarSummary{{Name: weekdays.Name, Description: weekdays.Description, Builtin: true}}

	names, err := calendarFileNames(dir)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		summary := CalendarSummary{Name: name}
		if isReservedCalendarName(name) {
			summary.Err = fmt.Errorf("calendar file %s%s: %q is reserved", name, calendarFileExtension, name)
		} else if cal, err := LoadCalendar(dir, name); err != nil {
			summary.Err = err
		} else {
			summary.Description = cal.Description
			summary.Through = cal.Through
		}
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Name < summaries[j].Name })
	return summaries, nil
}

// calendarFileNames returns the calendar names of the calendar files in dir.
func calendarFileNames(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read calendars directory: %w", err)
	}
	var names []string
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), calendarFileExtension)
		if !ok || entry.IsDir() || ir.ValidateCalendarName(name) != nil {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

func isReservedCalendarName(name string) bool {
	_, builtin := ir.BuiltinCalendar(name)
	return builtin || name == ir.CalendarNone
}

// unknownCalendarError names the calendars that do exist.
func unknownCalendarError(dir, name string) error {
	available := []string{ir.CalendarWeekdays}
	if names, err := calendarFileNames(dir); err == nil {
		available = append(available, names...)
	}
	slices.Sort(available)
	return fmt.Errorf("%w %q; available: %s", ErrUnknownCalendar, name, strings.Join(slices.Compact(available), ", "))
}

// calendarLoader loads calendar files and the calendars they include.
type calendarLoader struct {
	dir string
	// loading holds the calendars being loaded, to detect include cycles.
	loading map[string]bool
}

func (l *calendarLoader) load(name string) (*ir.Calendar, error) {
	if err := ir.ValidateCalendarName(name); err != nil {
		return nil, err
	}
	if isReservedCalendarName(name) {
		return nil, fmt.Errorf("calendar %q is reserved and has no file", name)
	}
	if l.loading[name] {
		return nil, fmt.Errorf("calendar %q includes itself", name)
	}
	if l.dir == "" {
		return nil, unknownCalendarError(l.dir, name)
	}
	data, err := os.ReadFile(filepath.Join(l.dir, name+calendarFileExtension)) //nolint:gosec // name matches the calendar name pattern, so the path stays in dir.
	if errors.Is(err, os.ErrNotExist) {
		return nil, unknownCalendarError(l.dir, name)
	}
	if err != nil {
		return nil, fmt.Errorf("read calendar %q: %w", name, err)
	}
	cal, includes, err := parseCalendar(name, data)
	if err != nil {
		return nil, fmt.Errorf("calendar %q: %w", name, err)
	}

	digest := sha256.New()
	digest.Write(data)
	l.loading[name] = true
	defer delete(l.loading, name)
	for _, include := range includes {
		included, err := l.load(include)
		if err != nil {
			return nil, fmt.Errorf("calendar %q: include %q: %w", name, include, err)
		}
		for day, holiday := range included.Holidays {
			if _, ok := cal.Holidays[day]; !ok {
				cal.Holidays[day] = holiday
			}
		}
		digest.Write([]byte(included.Digest))
	}
	cal.Digest = hex.EncodeToString(digest.Sum(nil))
	return cal, nil
}

// parseCalendar decodes a calendar file and returns the names it includes.
func parseCalendar(name string, data []byte) (*ir.Calendar, []string, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	cal := &ir.Calendar{
		Name:              name,
		Holidays:          map[ir.CivilDate]string{},
		ExtraBusinessDays: map[ir.CivilDate]struct{}{},
	}
	for day := time.Monday; day <= time.Friday; day++ {
		cal.Weekdays[day] = true
	}

	var includes []string
	for key, value := range raw {
		var err error
		switch key {
		case "description":
			description, ok := value.(string)
			if !ok {
				return nil, nil, fmt.Errorf("description must be a string")
			}
			cal.Description = description
		case "weekdays":
			cal.Weekdays, err = parseWeekdays(value)
		case "holidays":
			err = parseHolidays(value, cal.Holidays)
		case "extra_business_days":
			err = parseExtraBusinessDays(value, cal.ExtraBusinessDays)
		case "include":
			includes, err = parseIncludes(value)
		case "through":
			cal.Through, err = parseDateValue(value)
			if err != nil {
				err = fmt.Errorf("through: %w", err)
			}
		default:
			err = fmt.Errorf("unknown key %q", key)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return cal, includes, nil
}

func parseWeekdays(value any) ([7]bool, error) {
	var weekdays [7]bool
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return weekdays, fmt.Errorf("weekdays must be a non-empty list such as [MON, TUE, WED, THU, FRI]")
	}
	for _, item := range items {
		text, _ := item.(string)
		day, ok := weekdayNames[strings.ToUpper(strings.TrimSpace(text))]
		if !ok {
			return weekdays, fmt.Errorf("weekdays: unknown day %v; use MON, TUE, WED, THU, FRI, SAT, or SUN", item)
		}
		weekdays[day] = true
	}
	return weekdays, nil
}

func parseHolidays(value any, holidays map[ir.CivilDate]string) error {
	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("holidays must be a list")
	}
	for i, item := range items {
		if err := parseHoliday(item, holidays); err != nil {
			return fmt.Errorf("holidays[%d]: %w", i, err)
		}
	}
	return nil
}

// parseHoliday adds one holidays entry: a date, {date, name}, or
// {from, to, name}.
func parseHoliday(item any, holidays map[ir.CivilDate]string) error {
	entry, ok := item.(map[string]any)
	if !ok {
		day, err := parseDateValue(item)
		if err != nil {
			return err
		}
		holidays[day] = ""
		return nil
	}

	var (
		name           string
		day, from, to  ir.CivilDate
		hasDay, hasRng bool
	)
	for key, value := range entry {
		var err error
		switch key {
		case "name":
			name, ok = value.(string)
			if !ok {
				return fmt.Errorf("name must be a string")
			}
		case "date":
			day, err = parseDateValue(value)
			hasDay = true
		case "from":
			from, err = parseDateValue(value)
			hasRng = true
		case "to":
			to, err = parseDateValue(value)
			hasRng = true
		default:
			err = fmt.Errorf("unknown key %q; use date, or from and to, with an optional name", key)
		}
		if err != nil {
			return err
		}
	}

	switch {
	case hasDay && !hasRng:
		holidays[day] = name
	case hasRng && !hasDay:
		if from.IsZero() || to.IsZero() {
			return fmt.Errorf("a range needs both from and to")
		}
		if to.Before(from) {
			return fmt.Errorf("range ends on %s, before it starts on %s", to, from)
		}
		for current, days := from, 0; !to.Before(current); current, days = current.AddDays(1), days+1 {
			if days == maxHolidayRangeDays {
				return fmt.Errorf("range from %s to %s is longer than %d days", from, to, maxHolidayRangeDays)
			}
			holidays[current] = name
		}
	default:
		return fmt.Errorf("give either date, or from and to")
	}
	return nil
}

func parseExtraBusinessDays(value any, days map[ir.CivilDate]struct{}) error {
	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("extra_business_days must be a list of dates")
	}
	for i, item := range items {
		day, err := parseDateValue(item)
		if err != nil {
			return fmt.Errorf("extra_business_days[%d]: %w", i, err)
		}
		days[day] = struct{}{}
	}
	return nil
}

func parseIncludes(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("include must be a list of calendar names")
	}
	includes := make([]string, 0, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("include must be a list of calendar names")
		}
		includes = append(includes, name)
	}
	return includes, nil
}

// parseDateValue reads a date that YAML may decode as text or a timestamp.
func parseDateValue(value any) (ir.CivilDate, error) {
	switch v := value.(type) {
	case string:
		return ir.ParseCivilDate(strings.TrimSpace(v))
	case time.Time:
		if v.Hour() != 0 || v.Minute() != 0 || v.Second() != 0 {
			return ir.CivilDate{}, fmt.Errorf("invalid date %v: use YYYY-MM-DD", v)
		}
		return ir.DateOf(v), nil
	default:
		return ir.CivilDate{}, fmt.Errorf("invalid date %v: use YYYY-MM-DD", value)
	}
}

// calendarExpiryWarning reports a calendar whose holidays run out within
// CalendarExpiryWarningDays of today, or already have.
func calendarExpiryWarning(cal *ir.Calendar, today ir.CivilDate) string {
	if cal.Through.IsZero() {
		return ""
	}
	switch {
	case cal.Through.Before(today):
		return fmt.Sprintf("calendar %q lists holidays through %s; later dates count its weekdays alone, so add the next period's holidays",
			cal.Name, cal.Through)
	case cal.Through.Before(today.AddDays(CalendarExpiryWarningDays)):
		return fmt.Sprintf("calendar %q lists holidays through %s; add the next period's holidays before then",
			cal.Name, cal.Through)
	default:
		return ""
	}
}
