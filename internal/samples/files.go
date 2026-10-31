// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	fileExt        = ".jsonl"
	eventsFileName = "events.jsonl"
	maxLine        = 1 << 20
)

var tierDirs = [...]string{"raw", "minute", "hour", "day"}

var allTiers = [...]Tier{TierRaw, TierMinute, TierHour, TierDay}

func tierSec(t Tier) int64 { return int64(t.Width() / time.Second) }

func retentionSec(t Tier) int64 { return int64(t.Retention() / time.Second) }

func floorTo(sec, w int64) int64 {
	q := sec / w
	if sec%w < 0 {
		q--
	}
	return q * w
}

func ceilTo(sec, w int64) int64 {
	f := floorTo(sec, w)
	if f == sec {
		return f
	}
	return f + w
}

// fileSpan returns the span of the file of the tier that holds the time:
// an hour, a day, a month or a year.
func fileSpan(t Tier, sec int64) (start, end int64) {
	tm := time.Unix(sec, 0).UTC()
	var s, e time.Time
	switch t {
	case TierRaw:
		s = time.Date(tm.Year(), tm.Month(), tm.Day(), tm.Hour(), 0, 0, 0, time.UTC)
		e = s.Add(time.Hour)
	case TierMinute:
		s = time.Date(tm.Year(), tm.Month(), tm.Day(), 0, 0, 0, 0, time.UTC)
		e = s.AddDate(0, 0, 1)
	case TierHour:
		s = time.Date(tm.Year(), tm.Month(), 1, 0, 0, 0, 0, time.UTC)
		e = s.AddDate(0, 1, 0)
	case TierDay:
		fallthrough
	default:
		s = time.Date(tm.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		e = s.AddDate(1, 0, 0)
	}
	return s.Unix(), e.Unix()
}

var fileLayouts = [...]string{"2006010215", "20060102", "200601", "2006"}

func fileBase(t Tier, start int64) string {
	return time.Unix(start, 0).UTC().Format(fileLayouts[t]) + fileExt
}

func parseFileBase(t Tier, name string) (int64, bool) {
	base, ok := strings.CutSuffix(name, fileExt)
	if !ok {
		return 0, false
	}
	tm, err := time.Parse(fileLayouts[t], base)
	if err != nil {
		return 0, false
	}
	return tm.Unix(), true
}

type fileInfo struct {
	start, end int64
	path       string
}

// store is the file layer shared by the recorder and the reader.
type store struct {
	dir   string
	apps  [len(tierDirs)]*appender
	lastT map[string]int64 // last aggregate time of the files that were read or written
}

func newStore(dir string) *store {
	return &store{dir: dir, lastT: make(map[string]int64)}
}

func (s *store) path(t Tier, start int64) string {
	return filepath.Join(s.dir, tierDirs[t], fileBase(t, start))
}

func (s *store) eventsPath() string { return filepath.Join(s.dir, eventsFileName) }

// list returns the files of a tier, the oldest first. A missing directory
// gives an empty list.
func (s *store) list(t Tier) []fileInfo {
	entries, err := os.ReadDir(filepath.Join(s.dir, tierDirs[t]))
	if err != nil {
		return nil
	}
	var out []fileInfo
	for _, e := range entries {
		start, ok := parseFileBase(t, e.Name())
		if !ok || e.IsDir() {
			continue
		}
		_, end := fileSpan(t, start)
		out = append(out, fileInfo{start, end, s.path(t, start)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// listRange returns the files of a tier that overlap [lo, hi).
func (s *store) listRange(t Tier, lo, hi int64) []fileInfo {
	all := s.list(t)
	return slices.DeleteFunc(all, func(f fileInfo) bool { return f.end <= lo || f.start >= hi })
}

// appender appends to one file. The file opens when the first write comes.
type appender struct {
	path string
	f    *os.File
}

func (a *appender) write(b []byte) error {
	if a.f == nil {
		if err := os.MkdirAll(filepath.Dir(a.path), 0o750); err != nil {
			return err
		}
		f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec
		if err != nil {
			return err
		}
		a.f = f
		// A crash can leave a line without its end. End it, so that the next
		// line is not joined to it.
		if st, err := f.Stat(); err == nil && st.Size() > 0 {
			last := make([]byte, 1)
			if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
				if _, err := f.Write([]byte{'\n'}); err != nil {
					return err
				}
			}
		}
	}
	_, err := a.f.Write(b)
	return err
}

func (a *appender) sync() error {
	if a.f == nil {
		return nil
	}
	return a.f.Sync()
}

func (a *appender) close() error {
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return err
}

// appendTo appends to a file of the tier. It keeps one open file for each tier.
func (s *store) appendTo(t Tier, path string, b []byte) error {
	a := s.apps[t]
	if a == nil || a.path != path {
		if a != nil {
			if err := a.close(); err != nil {
				return err
			}
		}
		a = &appender{path: path}
		s.apps[t] = a
	}
	return a.write(b)
}

// forget closes the open file of a path, before the file is replaced or removed.
func (s *store) forget(path string) {
	for i, a := range s.apps {
		if a != nil && a.path == path {
			_ = a.close()
			s.apps[i] = nil
		}
	}
	delete(s.lastT, path)
}

func (s *store) syncAll() error {
	var errs []error
	for _, a := range s.apps {
		if a != nil {
			errs = append(errs, a.sync())
		}
	}
	return errors.Join(errs...)
}

func (s *store) closeAll() error {
	var errs []error
	for i, a := range s.apps {
		if a != nil {
			errs = append(errs, a.close())
			s.apps[i] = nil
		}
	}
	return errors.Join(errs...)
}

// eachLine calls fn for each line of a file until fn returns false. A missing
// file has no line.
func eachLine(path string, fn func(line []byte) bool) error {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 && !fn(sc.Bytes()) {
			return nil
		}
	}
	return sc.Err()
}

// lineTime reads the t field of an aggregate line without a full decode.
func lineTime(line []byte) (int64, bool) {
	const prefix = `{"t":`
	if !bytes.HasPrefix(line, []byte(prefix)) {
		return 0, false
	}
	rest := line[len(prefix):]
	n := 0
	for n < len(rest) && (rest[n] == '-' || (rest[n] >= '0' && rest[n] <= '9')) {
		n++
	}
	v, err := strconv.ParseInt(string(rest[:n]), 10, 64)
	return v, err == nil
}

// scanAggs calls fn for the aggregates with lo <= T < hi of a file, in order.
func scanAggs(path string, lo, hi int64, fn func(a agg)) error {
	return eachLine(path, func(line []byte) bool {
		if t, ok := lineTime(line); ok {
			if t < lo {
				return true
			}
			if t >= hi {
				return false
			}
		}
		if a, ok := parseAgg(line); ok && a.T >= lo && a.T < hi {
			fn(a)
		}
		return true
	})
}

// aggCount is the part of an aggregate that the availability and the streak
// read.
type aggCount struct {
	t    int64
	n, f uint64
}

// aggCountCacheSize bounds the cached files; the counts of a minute file take
// about 35 KB.
const aggCountCacheSize = 128

// aggCounts keeps the counts of aggregate files across queries, because the
// availability reads a week of minute files that rarely change.
var aggCounts = fileutil.NewCache[[]aggCount]("samples_agg_counts", aggCountCacheSize, 0)

// readAggCounts returns the counts of the aggregates of a file, in order. A
// file is read again when its size or modification time changes.
func readAggCounts(path string) ([]aggCount, error) {
	counts, err := aggCounts.LoadLatest(path, func() ([]aggCount, error) {
		var out []aggCount
		err := scanAggs(path, -1<<62, 1<<62, func(a agg) { out = append(out, aggCount{a.T, a.N, a.F}) })
		return out, err
	})
	if errors.Is(err, fs.ErrNotExist) {
		aggCounts.Invalidate(path)
		return nil, nil
	}
	return counts, err
}

// writeFile replaces a file. The write goes to a temporary file that is
// synced, then renamed.
func writeFile(path string, buf []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + tmpSuffix
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec
	if err != nil {
		return err
	}
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

const tmpSuffix = ".tmp"

// lastOf returns the time of the last aggregate of a file. It reads only the
// time of each line, and keeps the answer.
func (s *store) lastOf(path string) (int64, error) {
	if last, ok := s.lastT[path]; ok {
		return last, nil
	}
	last := int64(minInt)
	err := eachLine(path, func(line []byte) bool {
		if t, ok := lineTime(line); ok {
			last = max(last, t)
		}
		return true
	})
	if err != nil {
		return 0, err
	}
	s.lastT[path] = last
	return last, nil
}

// putAgg stores an aggregate.
func (s *store) putAgg(t Tier, a *agg) error {
	return s.putAggs(t, []agg{*a})
}

// putAggs stores aggregates, which can be of more than one file. It replaces
// the records of the same bucket, and writes a file only when a record changes.
func (s *store) putAggs(t Tier, aggs []agg) error {
	byFile := make(map[int64][]agg)
	for _, a := range aggs {
		start, _ := fileSpan(t, a.T)
		byFile[start] = append(byFile[start], a)
	}
	for start, list := range byFile {
		if err := s.putFile(t, start, list); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) putFile(t Tier, start int64, list []agg) error {
	path := s.path(t, start)
	slices.SortFunc(list, func(x, y agg) int { return cmpInt(x.T, y.T) })
	last, err := s.lastOf(path)
	if err != nil {
		return err
	}
	if list[0].T > last {
		// All records are new and newer than the file: append, without a read.
		var buf []byte
		for i := range list {
			buf = list[i].appendJSON(buf)
		}
		if err := s.appendTo(t, path, buf); err != nil {
			return err
		}
		s.lastT[path] = list[len(list)-1].T
		return nil
	}
	// The file holds records of the same buckets, or older ones. Compare the
	// text of the lines, so that an unchanged file is not decoded or written.
	lines := make(map[int64][]byte)
	err = eachLine(path, func(line []byte) bool {
		if t, ok := lineTime(line); ok && bytes.HasSuffix(line, []byte("]}")) {
			lines[t] = append(bytes.Clone(line), '\n')
		}
		return true
	})
	if err != nil {
		return err
	}
	changed := false
	for i := range list {
		enc := list[i].appendJSON(nil)
		if old, found := lines[list[i].T]; !found || !bytes.Equal(old, enc) {
			lines[list[i].T] = enc
			changed = true
		}
	}
	if !changed {
		return nil
	}
	times := make([]int64, 0, len(lines))
	for tm := range lines {
		times = append(times, tm)
	}
	slices.Sort(times)
	var buf []byte
	for _, tm := range times {
		buf = append(buf, lines[tm]...)
	}
	s.forget(path)
	if err := writeFile(path, buf); err != nil {
		return err
	}
	s.lastT[path] = times[len(times)-1]
	return nil
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// parseRaw decodes a raw line: the offset in milliseconds, the duration in
// microseconds and the exit code. The message is not read.
// nolint:revive
func parseRaw(line []byte) (off, us int64, exit int, ok bool) {
	if len(line) < 7 || line[0] != '[' {
		return
	}
	i := 1
	var vals [3]int64
	for k := range vals {
		neg := false
		if i < len(line) && line[i] == '-' {
			neg = true
			i++
		}
		start := i
		var v int64
		for i < len(line) && line[i] >= '0' && line[i] <= '9' {
			v = v*10 + int64(line[i]-'0')
			i++
		}
		if i == start || i >= len(line) {
			return
		}
		if neg {
			v = -v
		}
		vals[k] = v
		if k < 2 && line[i] != ',' {
			return
		}
		if k == 2 && line[i] != ',' && line[i] != ']' {
			return
		}
		i++
	}
	return vals[0], vals[1], int(vals[2]), true
}

// appendRaw appends a raw line, with the end of line. The message is written
// only when it differs from lastMsg, which the caller keeps for the file.
func appendRaw(b []byte, off, us int64, exit int, msg string, lastMsg *string) []byte {
	b = append(b, '[')
	b = strconv.AppendInt(b, off, 10)
	b = append(b, ',')
	b = strconv.AppendInt(b, us, 10)
	b = append(b, ',')
	b = strconv.AppendInt(b, int64(exit), 10)
	if exit != 0 && msg != *lastMsg {
		b = append(b, ',')
		b = strconv.AppendQuote(b, msg)
		*lastMsg = msg
	}
	return append(b, "]\n"...)
}

// scanRaw calls fn for the samples of a raw file, with their absolute time in
// milliseconds, until fn returns false.
func scanRaw(f fileInfo, fn func(ms, us int64, exit int) bool) error {
	base := f.start * 1000
	return eachLine(f.path, func(line []byte) bool {
		off, us, exit, ok := parseRaw(line)
		if !ok {
			return true
		}
		return fn(base+off, us, exit)
	})
}

// newestRaw returns the time of the newest sample of the raw tier, in
// milliseconds.
func (s *store) newestRaw() (int64, bool) {
	for _, file := range slices.Backward(s.list(TierRaw)) {
		f, err := os.Open(file.path) //nolint:gosec
		if err != nil {
			continue
		}
		var ms int64
		found := false
		if st, err := f.Stat(); err == nil {
			_ = scanLinesBackward(f, st.Size(), func(_ int64, line []byte) bool {
				if off, _, _, ok := parseRaw(line); ok {
					ms, found = file.start*1000+off, true
					return false
				}
				return true
			})
		}
		_ = f.Close()
		if found {
			return ms, true
		}
	}
	return 0, false
}

// scanLinesBackward calls fn for the lines of a file before the offset end,
// the last line first, until fn returns false. It reads the file in blocks.
func scanLinesBackward(f *os.File, end int64, fn func(off int64, line []byte) bool) error {
	const block = 64 << 10
	pos := end
	var carry []byte
	for pos > 0 {
		start := max(pos-block, 0)
		buf := make([]byte, pos-start, int(pos-start)+len(carry))
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		data := append(buf, carry...)
		hi := len(data)
		for {
			i := bytes.LastIndexByte(data[:hi], '\n')
			if i < 0 {
				break
			}
			if seg := data[i+1 : hi]; len(seg) > 0 && !fn(start+int64(i+1), seg) {
				return nil
			}
			hi = i
		}
		if start == 0 {
			if hi > 0 {
				fn(0, data[:hi])
			}
			return nil
		}
		carry = bytes.Clone(data[:hi])
		pos = start
	}
	return nil
}

func errf(format string, a ...any) error { return fmt.Errorf("samples: "+format, a...) }
