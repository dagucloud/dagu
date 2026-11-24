// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	// feedMaxLineBytes is the longest line a feed returns as an entry.
	feedMaxLineBytes = 1 << 20
	// feedMaxReadBytes caps the bytes one feed read consumes.
	feedMaxReadBytes = 4 << 20
	// feedBufferBytes is the read buffer of one file.
	feedBufferBytes = 64 << 10
)

// Feed reads the audit files back after a saved position, for a reader that
// sends them elsewhere. It returns only lines that end in a newline, so a
// line still being written waits for a later read.
type Feed struct {
	dir string
}

// feedPosition is a Feed's position: a byte offset in each day file it has
// seen on disk.
type feedPosition struct {
	// Since is when the position started. Cleanup records written before it
	// describe files the reader never had to read.
	Since time.Time            `json:"since"`
	Files map[string]*feedFile `json:"files"`
	// Listing reports that a read of every file stopped at its limit; the
	// next read goes on with every file.
	Listing bool `json:"listing,omitempty"`
}

// feedFile is the position in one day file.
type feedFile struct {
	Offset int64 `json:"offset"`
	// LastAt is the timestamp of the last entry read from the file.
	LastAt time.Time `json:"last_at,omitzero"`
	// Recorded is the size a cleanup record gave the file before removing
	// it. Bytes past the offset and within it were lost.
	Recorded *int64 `json:"recorded,omitempty"`
	// RejectedFrom starts a run of bad lines that no entry has closed yet.
	RejectedFrom *time.Time `json:"rejected_from,omitempty"`
	// Oversized reports that the offset is inside a line longer than
	// feedMaxLineBytes, whose rest is passed over.
	Oversized bool `json:"oversized,omitempty"`
	// Seen is the size the file had when a read last reached its end. Past
	// the offset, it holds a line without its newline yet, which is read
	// again only once the file grows.
	Seen int64 `json:"seen,omitempty"`
}

// feedRead is the state of one Read.
type feedRead struct {
	dir   string
	pos   *feedPosition
	keep  func(*audit.Entry) bool
	limit int
	// today is the start of the current day.
	today time.Time
	kept  int
	bytes int64
	chunk audit.FeedChunk
}

// NewFeed returns a feed over the audit files in dir.
func NewFeed(dir string) *Feed {
	return &Feed{dir: dir}
}

// Read returns the entries after q.Position. A read without Full checks only
// today's file, with one stat. A Full read lists every day file, and once it
// has read today's file to its end, reports each file removed with entries it
// never read as a deleted loss. A first read, and the reads after a full one
// that stopped at its limit, are full.
func (f *Feed) Read(_ context.Context, q audit.FeedQuery) (audit.FeedChunk, error) {
	pos, fresh := decodeFeedPosition(q.Position, q.Now)
	today := q.Now.UTC().Format(dateFormat) + auditFileExtension
	todayStart, _ := fileDay(today)
	r := &feedRead{dir: f.dir, pos: pos, keep: q.Keep, limit: q.Limit, today: todayStart}
	full := q.Full || fresh || pos.Listing

	var sizes map[string]int64
	var err error
	if full {
		sizes, err = f.list()
	} else {
		sizes, err = f.stat(today)
	}
	if err != nil {
		return audit.FeedChunk{}, err
	}
	for name := range sizes {
		if pos.Files[name] == nil {
			pos.Files[name] = &feedFile{}
		}
	}

	// Today's file goes last, so the cleanup records it holds are read
	// before removed files are reported.
	names := make([]string, 0, len(sizes))
	for name := range sizes {
		if name != today {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if _, ok := sizes[today]; ok {
		names = append(names, today)
	}

	for _, name := range names {
		more, err := r.readFile(name, sizes[name])
		if err != nil {
			return audit.FeedChunk{}, err
		}
		if more {
			r.chunk.More = true
			break
		}
	}
	if full && !r.chunk.More {
		r.reportRemoved(sizes)
	}
	pos.Listing = full && r.chunk.More

	r.chunk.Position, err = json.Marshal(pos)
	if err != nil {
		return audit.FeedChunk{}, fmt.Errorf("fileaudit: encode feed position: %w", err)
	}
	return r.chunk, nil
}

// Unsent returns how many bytes of the day files lie past position: entries
// not read yet, and a line still being written. A file position does not hold
// counts whole; an empty position counts every file.
func (f *Feed) Unsent(_ context.Context, position []byte) (int64, error) {
	pos, _ := decodeFeedPosition(position, time.Time{})
	sizes, err := f.list()
	if err != nil {
		return 0, err
	}
	var unsent int64
	for name, size := range sizes {
		var offset int64
		if file := pos.Files[name]; file != nil {
			offset = file.Offset
		}
		unsent += max(size-offset, 0)
	}
	return unsent, nil
}

// decodeFeedPosition decodes a saved position. An empty or unreadable one
// starts at now with no file read, and reports true.
func decodeFeedPosition(data []byte, now time.Time) (*feedPosition, bool) {
	var pos feedPosition
	if len(data) == 0 || json.Unmarshal(data, &pos) != nil || pos.Since.IsZero() {
		return &feedPosition{Since: now.UTC(), Files: map[string]*feedFile{}}, true
	}
	if pos.Files == nil {
		pos.Files = map[string]*feedFile{}
	}
	return &pos, false
}

// list returns the size of every day file in the directory.
func (f *Feed) list() (map[string]int64, error) {
	entries, err := fileutil.ReadDir(f.dir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fileaudit: list audit files: %w", err)
	}
	sizes := make(map[string]int64, len(entries))
	for _, entry := range entries {
		if _, ok := fileDay(entry.Name()); !ok || entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			// Removed since the listing.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("fileaudit: stat audit file: %w", err)
		}
		sizes[entry.Name()] = info.Size()
	}
	return sizes, nil
}

// stat returns the size of one day file, or nothing while it does not exist.
func (f *Feed) stat(name string) (map[string]int64, error) {
	info, err := fileutil.Stat(filepath.Join(f.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fileaudit: stat audit file: %w", err)
	}
	return map[string]int64{name: info.Size()}, nil
}

// readFile reads the complete lines of one file past its offset, up to the
// read's limits, and reports whether it stopped at one.
func (r *feedRead) readFile(name string, size int64) (bool, error) {
	day, _ := fileDay(name)
	file := r.pos.Files[name]
	if file == nil {
		file = &feedFile{}
		r.pos.Files[name] = file
	}
	if size < file.Offset {
		// Replaced or cut by hand: what it held past the new size is lost.
		r.lose(audit.LossDeleted, file.since(day), day.AddDate(0, 0, 1))
		*file = feedFile{}
	}
	if size == file.Offset || size == file.Seen {
		r.closeRejected(file, day)
		return false, nil
	}

	fh, err := fileutil.Open(filepath.Join(r.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		// Removed since the listing; the next full read reports it.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("fileaudit: open audit file: %w", err)
	}
	defer func() { _ = fh.Close() }()
	if _, err := fh.Seek(file.Offset, io.SeekStart); err != nil {
		return false, fmt.Errorf("fileaudit: seek audit file: %w", err)
	}
	reader := bufio.NewReaderSize(io.LimitReader(fh, size-file.Offset), feedBufferBytes)

	for {
		if r.kept >= r.limit || r.bytes >= feedMaxReadBytes {
			return true, nil
		}
		if file.Oversized {
			n, done, err := skipLine(reader, feedMaxReadBytes-r.bytes)
			file.Offset += n
			r.bytes += n
			if done {
				file.Oversized = false
				r.reject(file, day)
			}
			if errors.Is(err, io.EOF) {
				file.Seen = size
				r.closeRejected(file, day)
				return false, nil
			}
			if err != nil {
				return false, fmt.Errorf("fileaudit: read audit file: %w", err)
			}
			continue
		}

		line, n, err := readLine(reader)
		switch {
		case errors.Is(err, errLineTooLong):
			file.Offset += n
			r.bytes += n
			file.Oversized = true
			continue
		case errors.Is(err, io.EOF):
			// The start of a line without its newline yet: read, but not
			// consumed.
			r.bytes += n
			file.Seen = size
			r.closeRejected(file, day)
			return false, nil
		case err != nil:
			return false, fmt.Errorf("fileaudit: read audit file: %w", err)
		}
		file.Offset += n
		r.bytes += n

		entry, ok := parseEntry(line)
		if !ok {
			r.reject(file, day)
			continue
		}
		if file.RejectedFrom != nil {
			r.lose(audit.LossRejected, *file.RejectedFrom, entry.Timestamp)
			file.RejectedFrom = nil
		}
		file.LastAt = entry.Timestamp
		r.applyCleanup(entry)
		if r.keep == nil || r.keep(entry) {
			r.chunk.Entries = append(r.chunk.Entries, entry)
			r.chunk.Bytes += n
			r.kept++
		}
	}
}

// reject opens a run of bad lines, from the entry before it, unless one is
// open. The entry after the run closes it.
func (r *feedRead) reject(file *feedFile, day time.Time) {
	if file.RejectedFrom == nil {
		from := file.since(day)
		file.RejectedFrom = &from
	}
}

// closeRejected reports an open run of bad lines that ends the file, as
// running to the end of its day. A run in today's file stays open, since the
// entry after it may still arrive.
func (r *feedRead) closeRejected(file *feedFile, day time.Time) {
	if file.RejectedFrom != nil && day.Before(r.today) {
		r.lose(audit.LossRejected, *file.RejectedFrom, day.AddDate(0, 0, 1))
		file.RejectedFrom = nil
	}
}

// errLineTooLong reports a line that passed feedMaxLineBytes before its
// newline.
var errLineTooLong = errors.New("audit line too long")

// readLine reads one line that ends in a newline and returns it without the
// newline, with the bytes it consumed; a complete line longer than
// feedMaxLineBytes returns nil. A line without a newline yet returns io.EOF
// with the bytes it read, which stay unconsumed, unless it has passed
// feedMaxLineBytes: then it returns errLineTooLong with the bytes it consumed.
func readLine(reader *bufio.Reader) ([]byte, int64, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		line = append(line, part...)
		n := int64(len(line))
		tooLong := len(line) > feedMaxLineBytes+1
		switch {
		case err == nil && tooLong:
			return nil, n, nil
		case err == nil:
			return line[:len(line)-1], n, nil
		case errors.Is(err, bufio.ErrBufferFull) && tooLong:
			return nil, n, errLineTooLong
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > feedMaxLineBytes:
			return nil, n, errLineTooLong
		case errors.Is(err, io.EOF):
			return nil, n, io.EOF
		default:
			return nil, 0, err
		}
	}
}

// skipLine passes over the rest of a line, reading at most about budget
// bytes. It returns the bytes it read and whether it reached the newline.
func skipLine(reader *bufio.Reader, budget int64) (int64, bool, error) {
	var n int64
	for n < budget {
		part, err := reader.ReadSlice('\n')
		n += int64(len(part))
		switch {
		case err == nil:
			return n, true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return n, false, err
		}
	}
	return n, false, nil
}

// parseEntry decodes a line, which must be an entry with an ID and a
// timestamp.
func parseEntry(line []byte) (*audit.Entry, bool) {
	if line == nil {
		return nil, false
	}
	var entry audit.Entry
	if json.Unmarshal(line, &entry) != nil || entry.ID == "" || entry.Timestamp.IsZero() {
		return nil, false
	}
	return &entry, true
}

// applyCleanup notes the sizes a cleanup record gives the files it removes.
// A file never seen is added at offset 0, as one written while no reader ran.
func (r *feedRead) applyCleanup(entry *audit.Entry) {
	if entry.Category != audit.CategorySystem || entry.Action != cleanupAction ||
		entry.Timestamp.Before(r.pos.Since) {
		return
	}
	var record cleanupRecord
	if json.Unmarshal([]byte(entry.Details), &record) != nil {
		return
	}
	for _, removed := range record.Files {
		if _, ok := fileDay(removed.Name); !ok {
			continue
		}
		file := r.pos.Files[removed.Name]
		if file == nil {
			file = &feedFile{}
			r.pos.Files[removed.Name] = file
		}
		file.Recorded = &removed.Bytes
	}
}

// reportRemoved reports each file gone from disk that held bytes past its
// offset, or whose size no cleanup record gave, and forgets it.
func (r *feedRead) reportRemoved(onDisk map[string]int64) {
	names := make([]string, 0, len(r.pos.Files))
	for name := range r.pos.Files {
		if _, ok := onDisk[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		file := r.pos.Files[name]
		day, _ := fileDay(name)
		if file.Recorded == nil || *file.Recorded > file.Offset {
			// Covers a run of bad lines still open in it too.
			r.lose(audit.LossDeleted, file.since(day), day.AddDate(0, 0, 1))
		} else {
			r.closeRejected(file, day)
		}
		delete(r.pos.Files, name)
	}
}

// lose reports a loss. Entries in a file need not be in time order, so the
// ends are ordered here.
func (r *feedRead) lose(reason string, from, to time.Time) {
	if to.Before(from) {
		from, to = to, from
	}
	r.chunk.Losses = append(r.chunk.Losses, audit.FeedLoss{Reason: reason, From: from, To: to})
}

// since returns where entries not yet read from the file start: after the
// last entry read, or at the start of its day.
func (f *feedFile) since(day time.Time) time.Time {
	if f.LastAt.IsZero() {
		return day
	}
	return f.LastAt
}

// fileDay returns the day a day file's name stands for.
func fileDay(name string) (time.Time, bool) {
	date, ok := strings.CutSuffix(name, auditFileExtension)
	if !ok {
		return time.Time{}, false
	}
	day, err := time.Parse(dateFormat, date)
	return day, err == nil
}
