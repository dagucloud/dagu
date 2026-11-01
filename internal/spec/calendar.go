// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/goccy/go-yaml"
)

const (
	// CalendarFileExtension is the extension of calendar files.
	CalendarFileExtension = ".yaml"
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
	return NewCalendarSource(dir).Calendar(name)
}

// ListCalendars lists the built-in calendars and the calendar files in dir,
// sorted by name. A missing dir lists only the built-in calendars.
func ListCalendars(dir string) ([]CalendarSummary, error) {
	return NewCalendarSource(dir).List()
}

// CalendarSource loads the calendar files of one directory. It keeps each
// parsed calendar until its file, or a file it includes, changes identity,
// size or modification time, or until Invalidate, so DAGs that share a
// calendar parse it once. The content of the files is rechecked too, at
// most once a second per calendar, for rewrites that keep all three. A file
// that cannot be read is retried on every call. The calendars it returns
// are shared and must not be modified.
type CalendarSource struct {
	dir     string
	mu      sync.Mutex
	entries map[string]calendarEntry
	// contentRecheck is how long a calendar is used before the content of
	// its files is read again.
	contentRecheck time.Duration
}

// calendarContentRecheck bounds how long a rewrite that keeps a file's
// identity, size and modification time goes unnoticed.
const calendarContentRecheck = time.Second

// calendarEntry is a loaded calendar, or why it did not load, with the
// files it was read from.
type calendarEntry struct {
	cal   *ir.Calendar
	err   error
	files []fileStamp
	// checkedAt is when the content of the files was last read.
	checkedAt time.Time
}

// fileStamp identifies the version of a file, or that it was missing.
type fileStamp struct {
	path string
	// info is nil when the file was missing.
	info os.FileInfo
	// digest is the SHA-256 of the content, nil unless the file was read.
	digest []byte
}

func statFile(path string) fileStamp {
	info, err := os.Stat(path) //nolint:gosec // paths are the calendars directory and names matching the calendar name pattern.
	if err != nil {
		return fileStamp{path: path}
	}
	return fileStamp{path: path, info: info}
}

// sameVersion reports whether two stamps of one path describe the same
// version. The file identity counts, as a symlink swap can point the path
// at a file of the same size and modification time.
func (f fileStamp) sameVersion(other fileStamp) bool {
	if f.info == nil || other.info == nil {
		return f.info == nil && other.info == nil
	}
	return os.SameFile(f.info, other.info) &&
		f.info.Size() == other.info.Size() &&
		f.info.ModTime().Equal(other.info.ModTime())
}

// current reports whether the files are unchanged, reading their content
// again once recheck has passed since it was last read.
func (e *calendarEntry) current(now time.Time, recheck time.Duration) bool {
	for _, file := range e.files {
		if !statFile(file.path).sameVersion(file) {
			return false
		}
	}
	if now.Sub(e.checkedAt) < recheck {
		return true
	}
	for _, file := range e.files {
		if file.digest == nil {
			continue
		}
		data, err := os.ReadFile(file.path) //nolint:gosec // paths are calendar files read by an earlier load.
		if err != nil {
			return false
		}
		if sum := sha256.Sum256(data); !bytes.Equal(sum[:], file.digest) {
			return false
		}
	}
	e.checkedAt = now
	return true
}

// NewCalendarSource returns a source for the calendar files in dir. With an
// empty dir, only built-in calendars exist.
func NewCalendarSource(dir string) *CalendarSource {
	return &CalendarSource{dir: dir, entries: map[string]calendarEntry{}, contentRecheck: calendarContentRecheck}
}

// Dir returns the directory of the calendar files.
func (s *CalendarSource) Dir() string {
	return s.dir
}

// Invalidate drops every cached calendar, so the next lookups read the files
// again. Lookups notice files that change identity, size or modification
// time; Invalidate covers a rewrite that keeps all three.
func (s *CalendarSource) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.entries)
}

// Calendar returns the named calendar: a built-in one, or the calendar file
// <name>.yaml with the holidays of the calendars it includes.
func (s *CalendarSource) Calendar(name string) (*ir.Calendar, error) {
	if cal, ok := ir.BuiltinCalendar(name); ok {
		return cal, nil
	}
	return s.CalendarFile(name)
}

// CalendarFile returns the calendar file <name>.yaml with the holidays of
// the calendars it includes. A reserved name is an error, even when a file
// has that name.
func (s *CalendarSource) CalendarFile(name string) (*ir.Calendar, error) {
	if isReservedCalendarName(name) {
		return nil, fmt.Errorf("calendar file %s%s: %q is reserved", name, CalendarFileExtension, name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if entry, ok := s.entries[name]; ok && entry.current(now, s.contentRecheck) {
		s.entries[name] = entry
		return entry.cal, entry.err
	}
	loader := calendarLoader{dir: s.dir, loading: map[string]bool{}, included: map[string]loadResult{}}
	cal, err := loader.load(name)
	if loader.readFailed {
		delete(s.entries, name)
	} else {
		s.entries[name] = calendarEntry{cal: cal, err: err, files: loader.files, checkedAt: now}
	}
	return cal, err
}

// List lists the built-in calendars and the calendar files, sorted by name.
// A missing directory lists only the built-in calendars.
func (s *CalendarSource) List() ([]CalendarSummary, error) {
	weekdays, _ := ir.BuiltinCalendar(ir.CalendarWeekdays)
	summaries := []CalendarSummary{{Name: weekdays.Name, Description: weekdays.Description, Builtin: true}}

	names, err := calendarFileNames(s.dir)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		summary := CalendarSummary{Name: name}
		if cal, err := s.CalendarFile(name); err != nil {
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
		name, ok := strings.CutSuffix(entry.Name(), CalendarFileExtension)
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
	// included holds the result of each included calendar, so calendars
	// that share includes read them once, not once per include path.
	included map[string]loadResult
	// files records each file read, or found missing, while loading.
	files []fileStamp
	// readFailed reports a file that exists but could not be read. The
	// failure can clear without the file changing, as with permissions.
	readFailed bool
}

// loadResult is a loaded calendar, or why it did not load.
type loadResult struct {
	cal *ir.Calendar
	err error
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
	path := filepath.Join(l.dir, name+CalendarFileExtension)
	// Stamped before reading, so a write during the read shows as a change.
	stamp := len(l.files)
	l.files = append(l.files, statFile(path))
	data, err := os.ReadFile(path) //nolint:gosec // name matches the calendar name pattern, so the path stays in dir.
	if errors.Is(err, os.ErrNotExist) {
		// The error lists the directory's calendars, so it lasts until the
		// directory changes.
		l.files = append(l.files, statFile(l.dir))
		return nil, unknownCalendarError(l.dir, name)
	}
	if err != nil {
		l.readFailed = true
		return nil, fmt.Errorf("read calendar %q: %w", name, err)
	}
	sum := sha256.Sum256(data)
	l.files[stamp].digest = sum[:]
	cal, includes, err := parseCalendar(name, data)
	if err != nil {
		return nil, fmt.Errorf("calendar %q: %w", name, err)
	}

	digest := sha256.New()
	digest.Write(data)
	l.loading[name] = true
	defer delete(l.loading, name)
	for _, include := range includes {
		included, err := l.include(include)
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

// include loads an included calendar, or returns its result from an earlier
// include. Any result shaped by a calendar still loading comes from an
// include cycle, which fails the calendar being loaded anyway.
func (l *calendarLoader) include(name string) (*ir.Calendar, error) {
	if result, ok := l.included[name]; ok {
		return result.cal, result.err
	}
	cal, err := l.load(name)
	l.included[name] = loadResult{cal: cal, err: err}
	return cal, err
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
	case !today.AddDays(CalendarExpiryWarningDays).Before(cal.Through):
		return fmt.Sprintf("calendar %q lists holidays through %s; add the next period's holidays before then",
			cal.Name, cal.Through)
	default:
		return ""
	}
}
