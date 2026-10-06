// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import "unicode/utf8"

// Bounds of a column profile, which keep its size fixed however many rows
// the column holds.
const (
	// maxProfileDistinct is the most distinct values a profile counts.
	maxProfileDistinct = 1000
	// maxProfileValues is the most distinct values a profile lists.
	maxProfileValues = 12
	// maxProfileText is the longest value or cell text a profile carries,
	// in runes.
	maxProfileText = 40
	// maxOddCells is how many odd cells a profile names.
	maxOddCells = 3
)

// columnProfile accumulates the profile of one column.
type columnProfile struct {
	info ColumnInfo
	kind ColumnType
	col  int // sheet column number
	// seen holds the distinct values as keyText, and first the first of
	// them in order of appearance.
	seen  map[string]struct{}
	first []string
	// low and high are the numeric bounds of an integer or number column.
	low, high float64
}

// profileColumns profiles each column over typed rows. reg is the region
// the rows were read from, so a column's position gives its sheet column.
func (w *file) profileColumns(reg region, headers []string, types map[string]string, rows []Row) []ColumnInfo {
	profiles := make([]*columnProfile, len(headers))
	for i, h := range headers {
		profiles[i] = &columnProfile{
			info: ColumnInfo{Name: h, Type: types[h]},
			kind: ColumnType(types[h]),
			col:  reg.C1 + i,
			seen: map[string]struct{}{},
		}
	}
	for _, row := range rows {
		if rowIsNull(row, headers) {
			continue
		}
		r, _ := row[RowNumberKey].(int)
		for i, h := range headers {
			profiles[i].add(row[h], r, w.date1904)
		}
	}
	columns := make([]ColumnInfo, len(profiles))
	for i, p := range profiles {
		columns[i] = p.result()
	}
	return columns
}

func (p *columnProfile) add(v any, row int, date1904 bool) {
	if isBlank(v) {
		p.info.Blank++
		return
	}
	p.info.Filled++
	if len(p.seen) < maxProfileDistinct {
		key := keyText(v)
		if _, ok := p.seen[key]; !ok {
			p.seen[key] = struct{}{}
			if len(p.first) < maxProfileValues {
				p.first = append(p.first, key)
			}
		}
	}
	// A string column reads every value, so none is odd.
	if p.kind == TypeString {
		return
	}
	read := v
	if detectKind(v) != string(p.kind) {
		var err error
		if read, err = coerce(v, p.kind, date1904); err != nil {
			p.info.Odd++
			if len(p.info.OddCells) < maxOddCells {
				p.info.OddCells = append(p.info.OddCells, OddCell{Cell: cellName(p.col, row), Text: shortText(valueString(v))})
			}
			return
		}
	}
	p.bound(read)
}

// bound widens Min and Max to a value read as the column's type.
func (p *columnProfile) bound(v any) {
	switch p.kind {
	case TypeInteger, TypeNumber:
		f, ok := toFloat(v)
		if !ok {
			return
		}
		if p.info.Min == nil || f < p.low {
			p.low, p.info.Min = f, v
		}
		if p.info.Max == nil || f > p.high {
			p.high, p.info.Max = f, v
		}
	case TypeDate, TypeDateTime:
		// ISO 8601 text sorts in time order.
		s := valueString(v)
		if p.info.Min == nil || s < p.info.Min.(string) {
			p.info.Min = s
		}
		if p.info.Max == nil || s > p.info.Max.(string) {
			p.info.Max = s
		}
	case TypeString, TypeBoolean:
		// Text and booleans have no range worth reporting.
	}
}

func (p *columnProfile) result() ColumnInfo {
	info := p.info
	info.Distinct = len(p.seen)
	if info.Distinct <= maxProfileValues && info.Distinct < info.Filled {
		info.Values = make([]string, len(p.first))
		for i, v := range p.first {
			info.Values[i] = shortText(v)
		}
	}
	return info
}

// profileRows returns the leading rows holding at most limit rows with a
// value, and whether a row with a value follows them.
func profileRows(rows []Row, headers []string, limit int) ([]Row, bool) {
	counted := 0
	for i, row := range rows {
		if rowIsNull(row, headers) {
			continue
		}
		if counted == limit {
			return rows[:i], true
		}
		counted++
	}
	return rows, false
}

// rowIsNull reports whether every cell of a typed row is empty, the rows a
// read keeps only between rows that hold a value.
func rowIsNull(row Row, headers []string) bool {
	for _, h := range headers {
		if row[h] != nil {
			return false
		}
	}
	return true
}

// shortText cuts s to maxProfileText runes, ending a cut text in "…".
func shortText(s string) string {
	if utf8.RuneCountInString(s) <= maxProfileText {
		return s
	}
	return string([]rune(s)[:maxProfileText-1]) + "…"
}
