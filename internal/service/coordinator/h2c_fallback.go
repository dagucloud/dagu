// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	// maxH2CWindows bounds the h2c windows of a host kept in memory and in
	// the record.
	maxH2CWindows = 64
	// h2cRecordFileMode keeps the record readable by its user only.
	h2cRecordFileMode = 0o600
)

// h2cWindows keeps the h2c windows of a process's clients that share a data
// directory, by coordinator address. It reads the record in file at first use
// and writes it only when a window opens or ends, so later processes on the
// host use h2c without another failed handshake. A data directory that hosts
// share holds each host's windows apart, since each host has its own network
// path.
type h2cWindows struct {
	file string
	host string

	mu     sync.Mutex
	loaded bool
	// records are the host's open windows: those of the record as last read,
	// and those this process opened.
	records map[string]h2cWindow
	// own are the windows this process opened that no write has saved yet.
	// A save writes them over the record, which keeps every other change, so
	// a window another process ended after it was saved stays ended.
	own map[string]h2cWindow
}

// h2cWindow is an h2c window for an address: until when it holds, what
// selected h2c, and the handshake error.
type h2cWindow struct {
	Until  time.Time `json:"until"`
	Source string    `json:"source"`
	Error  string    `json:"error,omitempty"`
}

// h2cRecord is the record of the windows, by host and then by address.
type h2cRecord map[string]map[string]h2cWindow

var (
	processWindowsMu sync.Mutex
	// processWindows holds one h2cWindows per record file, so every client of
	// the process that shares a data directory shares its windows.
	processWindows = make(map[string]*h2cWindows)
)

// sharedH2CWindows returns the windows of the process's clients whose record
// is file.
func sharedH2CWindows(file string) *h2cWindows {
	processWindowsMu.Lock()
	defer processWindowsMu.Unlock()
	windows, ok := processWindows[file]
	if !ok {
		host, _ := os.Hostname()
		windows = newH2CWindows(file, host)
		processWindows[file] = windows
	}
	return windows
}

func newH2CWindows(file, host string) *h2cWindows {
	return &h2cWindows{
		file:    file,
		host:    host,
		records: make(map[string]h2cWindow),
		own:     make(map[string]h2cWindow),
	}
}

// active returns the window open for address at now.
func (w *h2cWindows) active(address string, now time.Time) (h2cWindow, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked(now)
	window, ok := w.records[address]
	if !ok || !now.Before(window.Until) {
		return h2cWindow{}, false
	}
	return window, true
}

// open opens window for address, unless one is open at now, and reports
// whether it did.
func (w *h2cWindows) open(address string, window h2cWindow, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked(now)
	if current, ok := w.records[address]; ok && now.Before(current.Until) {
		return false
	}
	w.own[address] = window
	w.saveLocked(address, now)
	return true
}

// end ends the window of address.
func (w *h2cWindows) end(address string, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked(now)
	if _, ok := w.records[address]; ok {
		delete(w.own, address)
		w.saveLocked(address, now)
	}
}

func (w *h2cWindows) loadLocked(now time.Time) {
	if w.loaded {
		return
	}
	w.loaded = true
	if record, ok := w.read(); ok {
		w.records = bounded(record[w.host], now)
	}
}

// saveLocked writes the host's windows after a change to address: the
// windows in the record, which other processes may have changed, with the
// ones this process opened over them, so a window another process ended
// stays ended. Without a readable record, the windows in memory stand in
// for it. It keeps the open ones, at most maxH2CWindows, in memory and in
// the record.
func (w *h2cWindows) saveLocked(address string, now time.Time) {
	record, ok := w.read()
	base := w.records
	if ok {
		base = bounded(record[w.host], now)
	} else {
		record = make(h2cRecord)
	}
	if _, opened := w.own[address]; !opened {
		delete(base, address)
	}
	windows := maps.Clone(base)
	maps.Copy(windows, w.own)
	for address, window := range windows {
		if !now.Before(window.Until) {
			delete(windows, address)
			delete(w.own, address)
		}
	}
	for len(windows) > maxH2CWindows {
		soonest := soonestToEnd(windows)
		delete(windows, soonest)
		delete(w.own, soonest)
	}
	w.records = windows
	if w.file == "" {
		return
	}
	record[w.host] = windows
	for host, windows := range record {
		if len(windows) == 0 || allEnded(windows, now) {
			delete(record, host)
		}
	}
	// The record only saves later processes a failed handshake, so a
	// failed write changes no transport.
	if err := fileutil.MkdirAll(filepath.Dir(w.file), peerDirMode); err != nil {
		return
	}
	if fileutil.WriteJSONAtomic(w.file, record, h2cRecordFileMode) == nil {
		clear(w.own)
	}
}

// read returns the record, empty when there is none, and whether it could
// be read.
func (w *h2cWindows) read() (h2cRecord, bool) {
	if w.file == "" {
		return nil, false
	}
	data, err := fileutil.ReadFile(w.file)
	if errors.Is(err, fs.ErrNotExist) {
		return make(h2cRecord), true
	}
	if err != nil {
		return nil, false
	}
	var record h2cRecord
	if json.Unmarshal(data, &record) != nil {
		return nil, false
	}
	if record == nil {
		record = make(h2cRecord)
	}
	return record, true
}

// bounded returns the windows that end at most h2cWindowLength after now.
// A window that ends later was opened by a clock that ran ahead, so it is
// dropped, and the next save leaves it out of the record.
func bounded(windows map[string]h2cWindow, now time.Time) map[string]h2cWindow {
	limit := now.Add(h2cWindowLength)
	result := make(map[string]h2cWindow, len(windows))
	for address, window := range windows {
		if !window.Until.After(limit) {
			result[address] = window
		}
	}
	return result
}

func allEnded(windows map[string]h2cWindow, now time.Time) bool {
	for _, window := range windows {
		if now.Before(window.Until) {
			return false
		}
	}
	return true
}

func soonestToEnd(windows map[string]h2cWindow) string {
	var soonest string
	for key, window := range windows {
		if soonest == "" || window.Until.Before(windows[soonest].Until) {
			soonest = key
		}
	}
	return soonest
}
