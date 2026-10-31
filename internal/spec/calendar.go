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
	"regexp"
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
	// Includes names the calendars whose holidays it adds, also when the
	// calendar does not load.
	Includes []string
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
	cal *ir.Calendar
	err error
	// includes names the calendars the file includes, known even when
	// the calendar does not load.
	includes []string
	files    []fileStamp
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
	entry := s.entry(name)
	return entry.cal, entry.err
}

// entry loads calendar file name, or returns the cached result while the
// files it was read from are unchanged.
func (s *CalendarSource) entry(name string) calendarEntry {
	if isReservedCalendarName(name) {
		return calendarEntry{err: fmt.Errorf("calendar file %s%s: %q is reserved", name, CalendarFileExtension, name)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if entry, ok := s.entries[name]; ok && entry.current(now, s.contentRecheck) {
		s.entries[name] = entry
		return entry
	}
	loader := newCalendarLoader(s.dir)
	cal, err := loader.load(name)
	entry := calendarEntry{cal: cal, err: err, includes: loader.includes[name], files: loader.files, checkedAt: now}
	if loader.readFailed {
		delete(s.entries, name)
	} else {
		s.entries[name] = entry
	}
	return entry
}

// Check parses data as the content of calendar file <name>.yaml, with the
// holidays of the calendars it includes from the source's directory, and
// reports every problem found. A problem with a place in data is a
// *FieldError. Check returns the calendar built from the valid parts, or
// nil when data is not a calendar document. Nothing is cached.
func (s *CalendarSource) Check(name string, data []byte) (*ir.Calendar, []error) {
	if err := ir.ValidateCalendarName(name); err != nil {
		return nil, []error{err}
	}
	if isReservedCalendarName(name) {
		return nil, []error{fmt.Errorf("calendar file %s%s: %q is reserved", name, CalendarFileExtension, name)}
	}
	loader := newCalendarLoader(s.dir)
	return loader.parse(name, data)
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
		entry := s.entry(name)
		summary := CalendarSummary{Name: name, Includes: entry.includes, Err: entry.err}
		if entry.cal != nil {
			summary.Description = entry.cal.Description
			summary.Through = entry.cal.Through
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
	// includes records the include list of each calendar parsed, also of
	// calendars that fail to load.
	includes map[string][]string
}

func newCalendarLoader(dir string) *calendarLoader {
	return &calendarLoader{dir: dir, loading: map[string]bool{}, included: map[string]loadResult{}, includes: map[string][]string{}}
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
	cal, errs := l.parse(name, data)
	if len(errs) > 0 {
		return nil, calendarLoadError(name, errs)
	}
	return cal, nil
}

// parse builds calendar name from its file content and the calendars it
// includes. It reports every problem found and returns the calendar built
// from the valid parts, or nil when data is not a calendar document.
func (l *calendarLoader) parse(name string, data []byte) (*ir.Calendar, []error) {
	file, errs := parseCalendar(name, data)
	if file.cal == nil {
		return nil, errs
	}
	l.includes[name] = file.cal.Includes
	cal := file.cal
	digest := sha256.New()
	digest.Write(data)
	l.loading[name] = true
	defer delete(l.loading, name)
	for i, include := range cal.Includes {
		included, err := l.include(include)
		if err != nil {
			errs = append(errs, &FieldError{Path: file.includePaths[i], Err: err})
			continue
		}
		for day, holiday := range included.Holidays {
			if _, ok := cal.Holidays[day]; !ok {
				cal.Holidays[day] = holiday
			}
		}
		digest.Write([]byte(included.Digest))
	}
	cal.Digest = hex.EncodeToString(digest.Sum(nil))
	return cal, errs
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

// calendarLoadError reports the problems of calendar name as one error. It
// keeps only their text: their paths are in the calendar file, not in the
// document that names the calendar.
func calendarLoadError(name string, errs []error) error {
	if len(errs) == 1 {
		return fmt.Errorf("calendar %q: %s", name, errs[0])
	}
	return fmt.Errorf("calendar %q: %s (and %d more problems)", name, errs[0], len(errs)-1)
}

// CalendarTemplate returns the content of a new calendar file: Monday to
// Friday, no holidays yet, maintained through the end of year.
func CalendarTemplate(year int) []byte {
	return fmt.Appendf(nil, `description: Business days
weekdays: [MON, TUE, WED, THU, FRI]
# Each holiday is a date, {date, name}, or a range {from, to, name}.
holidays: []
# Business days whatever their weekday, even when they are holidays.
extra_business_days: []
# The last date the holidays are maintained for.
through: %04d-12-31
`, year)
}

// calendarFile is a decoded calendar file.
type calendarFile struct {
	// cal lacks the holidays of the calendars it includes.
	cal *ir.Calendar
	// includePaths locates each name of cal.Includes in the file.
	includePaths []string
}

// calendarParser decodes the keys of a calendar file, collecting each
// problem with its place in the file.
type calendarParser struct {
	file calendarFile
	errs []error
}

// parseCalendar decodes a calendar file. It reports every problem found,
// each a *FieldError where it has a place in the file, and returns the
// calendar built from the valid parts, or none when data is not a calendar
// document.
func parseCalendar(name string, data []byte) (calendarFile, []error) {
	var document any
	if err := yaml.UnmarshalWithOptions(data, &document, yaml.UseOrderedMap()); err != nil {
		return calendarFile{}, []error{err}
	}
	keys, ok := document.(yaml.MapSlice)
	if document != nil && !ok {
		return calendarFile{}, []error{errors.New("a calendar file must be a mapping with keys such as weekdays and holidays")}
	}

	cal := &ir.Calendar{
		Name:              name,
		Holidays:          map[ir.CivilDate]string{},
		ExtraBusinessDays: map[ir.CivilDate]struct{}{},
	}
	for day := time.Monday; day <= time.Friday; day++ {
		cal.Weekdays[day] = true
	}
	p := calendarParser{file: calendarFile{cal: cal}}
	for _, item := range lastValues(keys) {
		key := fmt.Sprint(item.Key)
		switch key {
		case "description":
			if description, ok := item.Value.(string); ok {
				cal.Description = description
			} else {
				p.fail(key, "must be text")
			}
		case "weekdays":
			p.weekdays(item.Value)
		case "holidays":
			p.holidays(item.Value)
		case "extra_business_days":
			p.extraBusinessDays(item.Value)
		case "include":
			p.includes(item.Value)
		case "through":
			if through, err := parseDateValue(item.Value); err != nil {
				p.report(key, err)
			} else {
				cal.Through = through
			}
		default:
			p.fail(yamlPathKey(key), "unknown key %q; use description, weekdays, holidays, extra_business_days, include, or through", key)
		}
	}
	return p.file, p.errs
}

func (p *calendarParser) report(path string, err error) {
	p.errs = append(p.errs, &FieldError{Path: path, Err: err})
}

func (p *calendarParser) fail(path, format string, args ...any) {
	p.report(path, fmt.Errorf(format, args...))
}

func (p *calendarParser) weekdays(value any) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		p.fail("weekdays", "must be a non-empty list such as [MON, TUE, WED, THU, FRI]")
		return
	}
	var weekdays [7]bool
	for i, item := range items {
		text, _ := item.(string)
		day, ok := weekdayNames[strings.ToUpper(strings.TrimSpace(text))]
		if !ok {
			p.fail(fmt.Sprintf("weekdays[%d]", i), "unknown day %v; use MON, TUE, WED, THU, FRI, SAT, or SUN", item)
			continue
		}
		weekdays[day] = true
	}
	p.file.cal.Weekdays = weekdays
}

func (p *calendarParser) holidays(value any) {
	items, ok := value.([]any)
	if !ok {
		p.fail("holidays", "must be a list")
		return
	}
	for i, item := range items {
		p.holiday(fmt.Sprintf("holidays[%d]", i), item)
	}
}

// holiday adds one holidays entry: a date, {date, name}, or
// {from, to, name}.
func (p *calendarParser) holiday(path string, item any) {
	entry, ok := item.(yaml.MapSlice)
	if !ok {
		day, err := parseDateValue(item)
		if err != nil {
			p.report(path, err)
			return
		}
		p.file.cal.Holidays[day] = ""
		return
	}

	var (
		name             string
		day, from, to    ir.CivilDate
		hasDay, hasRange bool
		failed           bool
	)
	for _, field := range lastValues(entry) {
		key := fmt.Sprint(field.Key)
		fieldPath := path + "." + yamlPathKey(key)
		var err error
		switch key {
		case "name":
			if name, ok = field.Value.(string); !ok {
				err = errors.New("must be text")
			}
		case "date":
			day, err = parseDateValue(field.Value)
			hasDay = true
		case "from":
			from, err = parseDateValue(field.Value)
			hasRange = true
		case "to":
			to, err = parseDateValue(field.Value)
			hasRange = true
		default:
			err = fmt.Errorf("unknown key %q; use date, or from and to, with an optional name", key)
		}
		if err != nil {
			p.report(fieldPath, err)
			failed = true
		}
	}
	if failed {
		return
	}

	switch {
	case hasDay && !hasRange:
		p.file.cal.Holidays[day] = name
	case hasRange && !hasDay:
		if from.IsZero() || to.IsZero() {
			p.fail(path, "a range needs both from and to")
			return
		}
		if to.Before(from) {
			p.fail(path, "range ends on %s, before it starts on %s", to, from)
			return
		}
		days := make([]ir.CivilDate, 0, maxHolidayRangeDays)
		for current := from; !to.Before(current); current = current.AddDays(1) {
			if len(days) == maxHolidayRangeDays {
				p.fail(path, "range from %s to %s is longer than %d days", from, to, maxHolidayRangeDays)
				return
			}
			days = append(days, current)
		}
		for _, current := range days {
			p.file.cal.Holidays[current] = name
		}
	default:
		p.fail(path, "give either date, or from and to")
	}
}

func (p *calendarParser) extraBusinessDays(value any) {
	items, ok := value.([]any)
	if !ok {
		p.fail("extra_business_days", "must be a list of dates")
		return
	}
	for i, item := range items {
		day, err := parseDateValue(item)
		if err != nil {
			p.report(fmt.Sprintf("extra_business_days[%d]", i), err)
			continue
		}
		p.file.cal.ExtraBusinessDays[day] = struct{}{}
	}
}

func (p *calendarParser) includes(value any) {
	items, ok := value.([]any)
	if !ok {
		p.fail("include", "must be a list of calendar names")
		return
	}
	for i, item := range items {
		path := fmt.Sprintf("include[%d]", i)
		name, ok := item.(string)
		if !ok {
			p.fail(path, "must be a calendar name")
			continue
		}
		p.file.cal.Includes = append(p.file.cal.Includes, name)
		p.file.includePaths = append(p.file.includePaths, path)
	}
}

// lastValues keeps the last value of each key, in the order of those
// values. A key that a merge key repeats takes the value it would take in a
// map.
func lastValues(items yaml.MapSlice) yaml.MapSlice {
	last := make(map[string]int, len(items))
	for i, item := range items {
		last[fmt.Sprint(item.Key)] = i
	}
	kept := make(yaml.MapSlice, 0, len(last))
	for i, item := range items {
		if last[fmt.Sprint(item.Key)] == i {
			kept = append(kept, item)
		}
	}
	return kept
}

// yamlPathKey writes key as a step of a YAML path, quoting a key the path
// syntax would otherwise split.
func yamlPathKey(key string) string {
	if calendarPathKeyRe.MatchString(key) {
		return key
	}
	return "'" + key + "'"
}

var calendarPathKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

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

// CalendarExpiryWarning reports a calendar whose holidays, listed through
// the given date, run out within CalendarExpiryWarningDays of today, or
// already have. It is empty otherwise.
func CalendarExpiryWarning(name string, through, today ir.CivilDate) string {
	if through.IsZero() {
		return ""
	}
	switch {
	case through.Before(today):
		return fmt.Sprintf("calendar %q lists holidays through %s; later dates count its weekdays alone, so add the next period's holidays",
			name, through)
	case !today.AddDays(CalendarExpiryWarningDays).Before(through):
		return fmt.Sprintf("calendar %q lists holidays through %s; add the next period's holidays before then",
			name, through)
	default:
		return ""
	}
}
