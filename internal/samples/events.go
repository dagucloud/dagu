// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

// repeatFailedAfter is the length of a failure after which a failed event
// repeats.
const repeatFailedAfter = time.Hour

// maxMessage is the longest message of a sample, in bytes.
const maxMessage = 256

func cutMessage(m string) string {
	if len(m) <= maxMessage {
		return m
	}
	return strings.ToValidUTF8(m[:maxMessage], "")
}

type eventJSON struct {
	T int64     `json:"t"` // unix milliseconds
	K EventKind `json:"k"`
	C int       `json:"c,omitempty"`
	M string    `json:"m,omitempty"`
	N uint64    `json:"n,omitempty"`
	O int64     `json:"o,omitempty"` // outage, milliseconds
	R string    `json:"r,omitempty"` // run ID
	G int64     `json:"g,omitempty"` // gap, milliseconds
}

func (e Event) encode() []byte {
	b, _ := json.Marshal(eventJSON{
		T: e.Time.UnixMilli(), K: e.Kind, C: e.ExitCode, M: e.Message,
		N: e.FailedCount, O: e.Outage.Milliseconds(),
		R: e.RunID, G: e.Gap.Milliseconds(),
	})
	return append(b, '\n')
}

func parseEvent(line []byte) (Event, bool) {
	var j eventJSON
	if err := json.Unmarshal(line, &j); err != nil || j.K == "" {
		return Event{}, false
	}
	return Event{
		Time: time.UnixMilli(j.T).UTC(), Kind: j.K, ExitCode: j.C, Message: j.M,
		FailedCount: j.N, Outage: time.Duration(j.O) * time.Millisecond,
		RunID: j.R, Gap: time.Duration(j.G) * time.Millisecond,
	}, true
}

// tracker is the state machine that turns samples into failed and recovered
// events.
type tracker struct {
	inOutage    bool
	outageStart int64 // ms
	failed      uint64
	lastFailed  int64 // ms of the last failed event
}

// observe gives the event of a sample, if there is one.
func (t *tracker) observe(ms int64, exit int, msg string) (Event, bool) {
	at := time.UnixMilli(ms).UTC()
	switch {
	case exit != 0 && !t.inOutage:
		t.inOutage, t.outageStart, t.failed, t.lastFailed = true, ms, 1, ms
		return Event{Time: at, Kind: EventFailed, ExitCode: exit, Message: msg, FailedCount: 1}, true
	case exit != 0:
		t.failed++
		if time.Duration(ms-t.lastFailed)*time.Millisecond >= repeatFailedAfter {
			t.lastFailed = ms
			return Event{Time: at, Kind: EventFailed, ExitCode: exit, Message: msg, FailedCount: t.failed}, true
		}
	case t.inOutage:
		t.inOutage = false
		return Event{
			Time: at, Kind: EventRecovered, FailedCount: t.failed,
			Outage: time.Duration(ms-t.outageStart) * time.Millisecond,
		}, true
	}
	return Event{}, false
}

// loadTracker restores the state of an outage from the end of the events file
// and the raw tier. It returns the time of the last event, in milliseconds, as
// well.
func (s *store) loadTracker() (t tracker, lastEvent int64, err error) {
	f, err := os.Open(s.eventsPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return t, 0, nil
		}
		return t, 0, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return t, 0, err
	}
	const maxScan = 8 << 20
	scanned := int64(0)
	first := true
	var earliest, newestFailed *Event
	var failed uint64
	var countAt int64 // ms of the event that gives failed
	err = scanLinesBackward(f, st.Size(), func(_ int64, line []byte) bool {
		scanned += int64(len(line)) + 1
		e, ok := parseEvent(line)
		if !ok {
			return true
		}
		if first {
			lastEvent, first = e.Time.UnixMilli(), false
		}
		switch e.Kind {
		case EventRecovered:
			return false
		case EventFailed:
			ev := e
			if newestFailed == nil {
				newestFailed = &ev
			}
			earliest = &ev
			if failed == 0 {
				failed, countAt = e.FailedCount, e.Time.UnixMilli()
			}
		case EventStopped:
			if failed == 0 {
				failed, countAt = e.FailedCount, e.Time.UnixMilli()
			}
		case EventStarted, EventGap:
		}
		return scanned < maxScan
	})
	if err != nil {
		return t, 0, err
	}
	if earliest != nil {
		// A crash writes no stopped event: the failed samples stored after the
		// event of the count are part of the outage too.
		more, err := s.failedAfter(countAt)
		if err != nil {
			return t, 0, err
		}
		t = tracker{
			inOutage: true, outageStart: earliest.Time.UnixMilli(),
			failed: failed + more, lastFailed: newestFailed.Time.UnixMilli(),
		}
	}
	return t, lastEvent, nil
}

// failedAfter returns the count of failed samples of the raw tier after a time,
// in milliseconds.
func (s *store) failedAfter(ms int64) (uint64, error) {
	var n uint64
	for _, f := range s.listRange(TierRaw, ms/1000, noLimit) {
		err := scanRaw(f, func(t, _ int64, exit int) bool {
			if t > ms && exit != 0 {
				n++
			}
			return true
		})
		if err != nil {
			return 0, err
		}
	}
	return n, nil
}

const cursorPrefix = "e"

// The events file can start with a header line {"b":N}. N is the count of
// bytes that a compaction dropped before the first kept event. A cursor is the
// offset of an event since the first event ever written, so it stays exact
// after a compaction. parseEvent ignores the header.
const (
	headerPrefix = `{"b":`
	// maxHeader is more than the longest header line.
	maxHeader = 32
)

func encodeHeader(base int64) []byte {
	return fmt.Appendf(nil, "%s%d}\n", headerPrefix, base)
}

// eventsBase reads the header of the start of an events file. It returns the
// dropped bytes and the length of the header line, with its end of line. A file
// with no header gives zero for both.
func eventsBase(head []byte) (base, hdr int64) {
	rest, ok := bytes.CutPrefix(head, []byte(headerPrefix))
	if !ok {
		return 0, 0
	}
	end := bytes.Index(rest, []byte("}\n"))
	if end < 0 {
		return 0, 0
	}
	v, err := strconv.ParseInt(string(rest[:end]), 10, 64)
	if err != nil || v < 0 {
		return 0, 0
	}
	return v, int64(len(headerPrefix) + end + 2)
}

func readBase(f *os.File) (base, hdr int64, err error) {
	buf := make([]byte, maxHeader)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, 0, err
	}
	base, hdr = eventsBase(buf[:n])
	return base, hdr, nil
}

// compactEvents replaces the events file with a file that holds its newest
// events, at most half of limit bytes, when the file is larger than limit. The
// cut is at the start of a line.
func (s *store) compactEvents(limit int64) error {
	st, err := os.Stat(s.eventsPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if st.Size() <= limit {
		return nil
	}
	b, err := os.ReadFile(s.eventsPath())
	if err != nil {
		return err
	}
	base, hdr := eventsBase(b)
	body := b[hdr:]
	n := int64(len(body))
	cut := n - max(limit/2-maxHeader, 0)
	if cut > 0 && cut < n && body[cut-1] != '\n' {
		if i := bytes.IndexByte(body[cut:], '\n'); i >= 0 {
			cut += int64(i) + 1
		} else {
			cut = n
		}
	}
	cut = min(max(cut, 0), n)
	return writeFile(s.eventsPath(), append(encodeHeader(base+cut), body[cut:]...))
}

func encodeCursor(off int64) string { return cursorPrefix + strconv.FormatInt(off, 36) }

func decodeCursor(c string) (int64, error) {
	rest, ok := strings.CutPrefix(c, cursorPrefix)
	if !ok {
		return 0, fmt.Errorf("%w %q", ErrInvalidCursor, c)
	}
	v, err := strconv.ParseInt(rest, 36, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%w %q", ErrInvalidCursor, c)
	}
	return v, nil
}
