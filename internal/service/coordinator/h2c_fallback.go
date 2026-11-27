// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	// maxH2CWindows bounds the h2c windows kept in memory and in the record.
	maxH2CWindows = 64
	// h2cRecordFileMode keeps the record readable by its user only.
	h2cRecordFileMode = 0o600
)

// h2cWindows keeps the h2c windows of a process's clients that share a data
// directory, by coordinator address. It reads the record in file at first use
// and writes it only when a window opens or ends, so later processes on the
// host use h2c without another failed handshake.
type h2cWindows struct {
	file string

	mu      sync.Mutex
	loaded  bool
	records map[string]h2cWindow
}

// h2cWindow is an h2c window for an address: until when it holds, what
// selected h2c, and the handshake error.
type h2cWindow struct {
	Until  time.Time `json:"until"`
	Source string    `json:"source"`
	Error  string    `json:"error,omitempty"`
}

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
		windows = &h2cWindows{file: file, records: make(map[string]h2cWindow)}
		processWindows[file] = windows
	}
	return windows
}

// active returns the window open for address at now.
func (w *h2cWindows) active(address string, now time.Time) (h2cWindow, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked()
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
	w.loadLocked()
	if current, ok := w.records[address]; ok && now.Before(current.Until) {
		return false
	}
	w.saveLocked(address, &window, now)
	return true
}

// end ends the window of address.
func (w *h2cWindows) end(address string, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loadLocked()
	if _, ok := w.records[address]; ok {
		w.saveLocked(address, nil, now)
	}
}

func (w *h2cWindows) loadLocked() {
	if w.loaded {
		return
	}
	w.loaded = true
	maps.Copy(w.records, w.read())
}

// saveLocked sets the window of address, or removes it when window is nil,
// on the windows in memory and in the record, which other processes may have
// changed. It keeps the open ones, at most maxH2CWindows, in memory and in
// the record.
func (w *h2cWindows) saveLocked(address string, window *h2cWindow, now time.Time) {
	records := maps.Clone(w.records)
	for key, record := range w.read() {
		if current, ok := records[key]; !ok || record.Until.After(current.Until) {
			records[key] = record
		}
	}
	if window != nil {
		records[address] = *window
	} else {
		delete(records, address)
	}
	for key, record := range records {
		if !now.Before(record.Until) {
			delete(records, key)
		}
	}
	for len(records) > maxH2CWindows {
		delete(records, soonestToEnd(records))
	}
	w.records = records
	if w.file == "" {
		return
	}
	// The record only saves later processes a failed handshake, so a
	// failed write changes no transport.
	if err := fileutil.MkdirAll(filepath.Dir(w.file), peerDirMode); err == nil {
		_ = fileutil.WriteJSONAtomic(w.file, records, h2cRecordFileMode)
	}
}

// read returns the windows in the record, or nil when there is none.
func (w *h2cWindows) read() map[string]h2cWindow {
	if w.file == "" {
		return nil
	}
	data, err := fileutil.ReadFile(w.file)
	if err != nil {
		return nil
	}
	var records map[string]h2cWindow
	if json.Unmarshal(data, &records) != nil {
		return nil
	}
	return records
}

func soonestToEnd(records map[string]h2cWindow) string {
	var soonest string
	for key, record := range records {
		if soonest == "" || record.Until.Before(records[soonest].Until) {
			soonest = key
		}
	}
	return soonest
}
