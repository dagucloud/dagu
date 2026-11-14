// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package data

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/datapath"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
)

// Kinds of value a condition compares a field as.
const (
	kindText   = "text"
	kindNumber = "number"
	kindDate   = "date"
)

// conditionKinds is the kind of value each condition compares.
var conditionKinds = map[string]string{
	"min": kindNumber, "max": kindNumber,
	"from": kindDate, "to": kindDate, "within": kindDate,
	"is": kindText, "any": kindText, "none": kindText,
}

// condition is what one field of a row must be for the row to pass. A field
// is read as the kind of value its condition compares.
type condition struct {
	kind     string
	min, max *float64
	// from and to bound a date; to is the last day of the period it names.
	from, to *time.Time
	// within keeps dates no older than this many days before the run's day.
	within *int
	is     *string
	any    []string
	none   []string
}

// sortKey orders rows by one field, descending when desc is set.
type sortKey struct {
	field string
	desc  bool
}

// filterSpec is the where, sort, and limit of a data.filter step.
type filterSpec struct {
	where map[string]condition
	sort  []sortKey
	limit int
}

// parseFilterSpec reads where, sort, and limit. When the DAG loads, a
// condition's value may still hold a reference such as ${params.MAX}, which
// resolves when the step runs; deferred accepts it then. A step that runs
// reads every value.
func parseFilterSpec(cfg config, deferred bool) (filterSpec, error) {
	if len(cfg.Where) == 0 {
		return filterSpec{}, fmt.Errorf("%w: where must name at least one field", errConfig)
	}
	spec := filterSpec{where: make(map[string]condition, len(cfg.Where)), limit: cfg.Limit}
	for _, field := range slices.Sorted(maps.Keys(cfg.Where)) {
		c, err := parseCondition(cfg.Where[field], deferred)
		if err != nil {
			return filterSpec{}, fmt.Errorf("%w: where.%s: %v", errConfig, field, err)
		}
		spec.where[field] = c
	}
	keys, err := parseSort(cfg.Sort)
	if err != nil {
		return filterSpec{}, fmt.Errorf("%w: sort: %v", errConfig, err)
	}
	spec.sort = keys
	if cfg.Limit < 0 {
		return filterSpec{}, fmt.Errorf("%w: limit must not be negative", errConfig)
	}
	return spec, nil
}

func parseCondition(raw any, deferred bool) (condition, error) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return condition{}, fmt.Errorf("must be a map of min, max, from, to, within, is, any, or none")
	}
	var c condition
	kinds := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		value := fields[name]
		if text, ok := value.(string); ok && deferred && strings.Contains(text, "${") {
			if kind, known := conditionKinds[name]; known {
				kinds[kind] = true
				continue
			}
		}
		switch name {
		case "min", "max":
			n, ok := numberOf(value)
			if !ok {
				return condition{}, fmt.Errorf("%s must be a number, got %v", name, value)
			}
			if name == "min" {
				c.min = &n
			} else {
				c.max = &n
			}
			kinds[kindNumber] = true
		case "from", "to":
			d, ok := dateOf(value)
			if !ok {
				return condition{}, fmt.Errorf("%s must be a date such as 1981-06 or 2026-11-01, got %v", name, value)
			}
			if name == "from" {
				start := d.start()
				c.from = &start
			} else {
				end := d.end()
				c.to = &end
			}
			kinds[kindDate] = true
		case "within":
			days, err := parseDays(fmt.Sprint(value))
			if err != nil {
				return condition{}, err
			}
			c.within = &days
			kinds[kindDate] = true
		case "is":
			text := stringutil.NormalizeText(fmt.Sprint(value))
			c.is = &text
			kinds[kindText] = true
		case "any", "none":
			list, err := textList(value)
			if err != nil {
				return condition{}, fmt.Errorf("%s: %w", name, err)
			}
			if name == "any" {
				c.any = list
			} else {
				c.none = list
			}
			kinds[kindText] = true
		default:
			return condition{}, fmt.Errorf("unknown condition %q; use min, max, from, to, within, is, any, or none", name)
		}
	}
	switch len(kinds) {
	case 0:
		return condition{}, fmt.Errorf("names no condition")
	case 1:
		c.kind = slices.Collect(maps.Keys(kinds))[0]
	default:
		return condition{}, fmt.Errorf("mixes number, date, and text conditions; give a field one kind")
	}
	return c, nil
}

func textList(value any) ([]string, error) {
	switch v := value.(type) {
	case string:
		return []string{stringutil.NormalizeText(v)}, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, stringutil.NormalizeText(fmt.Sprint(item)))
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("must list at least one text")
		}
		return out, nil
	default:
		return nil, fmt.Errorf("must be a text or a list of texts")
	}
}

// parseDays reads a span of days, such as 7d, 2w, or 7.
func parseDays(text string) (int, error) {
	text = strings.TrimSpace(strings.ToLower(text))
	multiplier := 1
	switch {
	case strings.HasSuffix(text, "w"):
		multiplier, text = 7, strings.TrimSuffix(text, "w")
	case strings.HasSuffix(text, "d"):
		text = strings.TrimSuffix(text, "d")
	}
	days, err := strconv.Atoi(text)
	if err != nil || days < 0 {
		return 0, fmt.Errorf("within must be a number of days, such as 7d or 2w")
	}
	return days * multiplier, nil
}

func parseSort(raw any) ([]sortKey, error) {
	var names []string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		names = []string{v}
	case []any:
		for _, item := range v {
			names = append(names, fmt.Sprint(item))
		}
	default:
		return nil, fmt.Errorf("must be a field or a list of fields, each with - before it to sort descending")
	}
	keys := make([]sortKey, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		desc := strings.HasPrefix(name, "-")
		name = strings.TrimSpace(strings.TrimPrefix(name, "-"))
		if name == "" {
			return nil, fmt.Errorf("names an empty field")
		}
		keys = append(keys, sortKey{field: name, desc: desc})
	}
	return keys, nil
}

// unreadable counts, by field, the rows left out because the field did not
// read as the kind of value its condition compares, with one such value.
type unreadable struct {
	count   map[string]int
	example map[string]string
}

func (e *executorImpl) runFilter(ctx context.Context) error {
	raw, err := e.readValue()
	if err != nil {
		return err
	}
	value, err := parseValue(e.cfg, raw)
	if err != nil {
		return err
	}
	if e.cfg.Select != "" {
		selected, ok := datapath.Select(ctx, "data.filter", value, e.cfg.Select)
		if !ok {
			return fmt.Errorf("data.filter: select %q matched nothing", e.cfg.Select)
		}
		value = selected
	}
	rows, ok := asSlice(value)
	if !ok {
		return fmt.Errorf("data.filter reads a list of rows; the input is not a list (use select to pick one)")
	}
	spec, err := parseFilterSpec(e.cfg, false)
	if err != nil {
		return err
	}
	today := time.Now()
	missed := unreadable{count: map[string]int{}, example: map[string]string{}}
	kept := make([]any, 0, len(rows))
	for _, row := range rows {
		fields, ok := asMap(row)
		if !ok {
			return fmt.Errorf("data.filter reads rows that are objects; a row is %T", row)
		}
		if spec.passes(fields, today, &missed) {
			kept = append(kept, row)
		}
	}
	spec.order(kept)
	if spec.limit > 0 && len(kept) > spec.limit {
		kept = kept[:spec.limit]
	}
	_, _ = fmt.Fprintf(e.stderr, "data.filter: kept %d of %d rows\n", len(kept), len(rows))
	for _, field := range slices.Sorted(maps.Keys(missed.count)) {
		_, _ = fmt.Fprintf(e.stderr, "data.filter: left out %d rows whose %s did not read as a %s, such as %q\n",
			missed.count[field], field, spec.where[field].kind, missed.example[field])
	}
	cfg := e.cfg
	if cfg.To == "" {
		cfg.To = formatJSON
	}
	return writeValue(e.stdout, cfg, kept)
}

// passes reports whether every condition holds for the row.
func (s filterSpec) passes(row map[string]any, today time.Time, missed *unreadable) bool {
	for _, field := range slices.Sorted(maps.Keys(s.where)) {
		c := s.where[field]
		holds, read := c.holds(row[field], today)
		if !read {
			missed.count[field]++
			if _, seen := missed.example[field]; !seen {
				missed.example[field] = stringifyText(row[field])
			}
		}
		if !holds {
			return false
		}
	}
	return true
}

// holds reports whether the condition holds for value, and whether value
// read as the kind of value the condition compares.
func (c condition) holds(value any, today time.Time) (holds, read bool) {
	switch c.kind {
	case kindNumber:
		n, ok := numberOf(value)
		if !ok {
			return false, false
		}
		return (c.min == nil || n >= *c.min) && (c.max == nil || n <= *c.max), true
	case kindDate:
		d, ok := dateOf(value)
		if !ok {
			return false, false
		}
		at := d.start()
		if c.from != nil && at.Before(*c.from) {
			return false, true
		}
		if c.to != nil && at.After(*c.to) {
			return false, true
		}
		if c.within != nil {
			oldest := time.Date(today.Year(), today.Month(), today.Day()-*c.within, 0, 0, 0, 0, time.UTC)
			if at.Before(oldest) {
				return false, true
			}
		}
		return true, true
	default:
		normalized := stringutil.NormalizeText(stringifyText(value))
		if c.is != nil && normalized != *c.is {
			return false, true
		}
		if len(c.any) > 0 && !slices.ContainsFunc(c.any, func(s string) bool { return strings.Contains(normalized, s) }) {
			return false, true
		}
		if slices.ContainsFunc(c.none, func(s string) bool { return strings.Contains(normalized, s) }) {
			return false, true
		}
		return true, true
	}
}

// order sorts rows by the sort keys, keeping the order rows came in among
// equals. A field is compared as its condition's kind of value, else as the
// kind most of its values read as; values that do not read as that kind
// come after the others in either direction, in text order.
func (s filterSpec) order(rows []any) {
	if len(s.sort) == 0 {
		return
	}
	keys := make([][]sortValue, len(s.sort))
	for i, key := range s.sort {
		keys[i] = sortValues(rows, key.field, s.where[key.field].kind)
	}
	at := make([]int, len(rows))
	for i := range at {
		at[i] = i
	}
	slices.SortStableFunc(at, func(a, b int) int {
		for i, key := range s.sort {
			if order := keys[i][a].compare(keys[i][b], key.desc); order != 0 {
				return order
			}
		}
		return 0
	})
	sorted := make([]any, len(rows))
	for i, row := range at {
		sorted[i] = rows[row]
	}
	copy(rows, sorted)
}

// sortValue is one row's value of a field rows are ordered by.
type sortValue struct {
	// read reports whether the value read as the field's kind.
	read bool
	// number is a number, or a date's first day in Unix seconds.
	number float64
	// text is the folded text of a text value or of one that did not read.
	text string
}

// compare orders v before w, or after when desc is set; a value that did not
// read comes after one that did either way.
func (v sortValue) compare(w sortValue, desc bool) int {
	if v.read != w.read {
		if v.read {
			return -1
		}
		return 1
	}
	order := cmp.Compare(v.number, w.number)
	if order == 0 {
		order = strings.Compare(v.text, w.text)
	}
	if desc {
		order = -order
	}
	return order
}

// sortValues reads each row's value of field as kind, or as the kind most of
// the values read as when kind is empty.
func sortValues(rows []any, field, kind string) []sortValue {
	values := make([]any, len(rows))
	for i, row := range rows {
		fields, _ := asMap(row)
		values[i] = fields[field]
	}
	if kind == "" {
		kind = likelyKind(values)
	}
	out := make([]sortValue, len(values))
	for i, value := range values {
		switch kind {
		case kindNumber:
			if n, ok := numberOf(value); ok {
				out[i] = sortValue{read: true, number: n}
				continue
			}
		case kindDate:
			if d, ok := dateOf(value); ok {
				out[i] = sortValue{read: true, number: float64(d.start().Unix())}
				continue
			}
		default:
			out[i] = sortValue{read: true, text: stringutil.NormalizeText(stringifyText(value))}
			continue
		}
		out[i] = sortValue{text: stringutil.NormalizeText(stringifyText(value))}
	}
	return out
}

// likelyKind is a date when at least half of values read as dates, else a
// number when any reads as one, else text. A date also reads as a number,
// its year, so dates are counted first.
func likelyKind(values []any) string {
	dates, numbers := 0, 0
	for _, value := range values {
		if _, ok := dateOf(value); ok {
			dates++
		}
		if _, ok := numberOf(value); ok {
			numbers++
		}
	}
	switch {
	case dates > 0 && dates*2 >= len(values):
		return kindDate
	case numbers > 0:
		return kindNumber
	default:
		return kindText
	}
}

// numberPart is a number as a page writes it, with a Japanese unit of ten
// thousand or a hundred million after it, as in 4,280万 or 1億2,000万.
var numberPart = regexp.MustCompile(`(-?\d+(?:,\d{3})*(?:\.\d+)?)(億|万|千)?`)

var numberUnits = map[string]float64{"億": 1e8, "万": 1e4, "千": 1e3}

// parseNumber reads the first number in text, with the parts of a number
// written in Japanese units summed: 4,280万円 reads as 42800000, 1億2,000万
// as 120000000, and 徒歩4分 as 4. Full-width digits read as digits.
func parseNumber(text string) (float64, bool) {
	s := stringutil.NormalizeText(text)
	matches := numberPart.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return 0, false
	}
	total, end, chained := 0.0, -1, false
	for i, m := range matches {
		// A part continues the number only right after a part with a unit.
		if i > 0 && (!chained || m[0] != end) {
			break
		}
		n, err := strconv.ParseFloat(strings.ReplaceAll(s[m[2]:m[3]], ",", ""), 64)
		if err != nil {
			return 0, false
		}
		chained = m[4] >= 0
		if chained {
			n *= numberUnits[s[m[4]:m[5]]]
		}
		total, end = total+n, m[1]
	}
	return total, true
}

func numberOf(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float64:
		return v, true
	default:
		return parseNumber(stringifyText(v))
	}
}

// Precisions of a date as written: a year, a month, or a day.
const (
	precisionYear = iota + 1
	precisionMonth
	precisionDay
)

// dateValue is a date as written, to the precision it was written with.
type dateValue struct {
	year, month, day int
	precision        int
}

// start is the first day of the period the date names.
func (d dateValue) start() time.Time {
	return time.Date(d.year, time.Month(max(d.month, 1)), max(d.day, 1), 0, 0, 0, 0, time.UTC)
}

// end is the last day of the period the date names.
func (d dateValue) end() time.Time {
	switch d.precision {
	case precisionYear:
		return time.Date(d.year, time.December, 31, 0, 0, 0, 0, time.UTC)
	case precisionMonth:
		return time.Date(d.year, time.Month(d.month)+1, 0, 0, 0, 0, 0, time.UTC)
	default:
		return d.start()
	}
}

var (
	// westernDate is 2026/11/12, 2026-11-12, 2026.11.12, 1983年5月, or
	// 2026年11月12日, with the day left out or not.
	westernDate = regexp.MustCompile(`(\d{4})\s*[年/.\-]\s*(\d{1,2})(?:\s*[月/.\-]\s*(\d{1,2}))?`)
	// eraDate is a date of a Japanese era, such as 昭和58年5月.
	eraDate = regexp.MustCompile(`(明治|大正|昭和|平成|令和)\s*(元|\d{1,2})\s*年(?:\s*(\d{1,2})\s*月(?:\s*(\d{1,2})\s*日)?)?`)
	// yearOnly is a year alone, such as 1983年 or 1983.
	yearOnly = regexp.MustCompile(`^(\d{4})\s*年?$|(\d{4})\s*年`)
)

// eraStart is the western year before the first year of each era.
var eraStart = map[string]int{"明治": 1867, "大正": 1911, "昭和": 1925, "平成": 1988, "令和": 2018}

// parseDate reads the first date in text: 2026/11/12, 2026-11-12, 1983年5月,
// 昭和58年5月, or a year alone, as in 1983年.
func parseDate(text string) (dateValue, bool) {
	s := stringutil.NormalizeText(text)
	if m := eraDate.FindStringSubmatch(s); m != nil {
		n := 1
		if m[2] != "元" {
			n, _ = strconv.Atoi(m[2])
		}
		d := dateValue{year: eraStart[m[1]] + n, precision: precisionYear}
		d.month, d.day, d.precision = dateParts(m[3], m[4], d.precision)
		return d, d.valid()
	}
	if m := westernDate.FindStringSubmatch(s); m != nil {
		year, _ := strconv.Atoi(m[1])
		d := dateValue{year: year, precision: precisionYear}
		d.month, d.day, d.precision = dateParts(m[2], m[3], d.precision)
		return d, d.valid()
	}
	if m := yearOnly.FindStringSubmatch(s); m != nil {
		year, _ := strconv.Atoi(cmp.Or(m[1], m[2]))
		d := dateValue{year: year, precision: precisionYear}
		return d, d.valid()
	}
	return dateValue{}, false
}

func dateParts(month, day string, precision int) (int, int, int) {
	m, d := 0, 0
	if month != "" {
		m, _ = strconv.Atoi(month)
		precision = precisionMonth
	}
	if day != "" {
		d, _ = strconv.Atoi(day)
		precision = precisionDay
	}
	return m, d, precision
}

func (d dateValue) valid() bool {
	if d.year < 1000 || d.year > 9999 || d.month < 0 || d.month > 12 {
		return false
	}
	switch d.precision {
	case precisionYear:
		return true
	case precisionMonth:
		return d.month >= 1
	default:
		// A day the month does not have, such as February 31, is no date.
		at := d.start()
		return d.month >= 1 && d.day >= 1 && at.Month() == time.Month(d.month) && at.Day() == d.day
	}
}

func dateOf(value any) (dateValue, bool) {
	if t, ok := value.(time.Time); ok {
		return dateValue{year: t.Year(), month: int(t.Month()), day: t.Day(), precision: precisionDay}, true
	}
	return parseDate(stringifyText(value))
}
