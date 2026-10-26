// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"sync"
	"time"
)

// ledgerSize bounds the events a gate keeps for its readers.
const ledgerSize = 4096

// ledger keeps the latest events, numbered from 1 in the order they
// happened.
type ledger struct {
	mu     sync.Mutex
	seq    int64
	events []Event
}

// add numbers and keeps an event.
func (l *ledger) add(event Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	event.Seq = l.seq
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if len(l.events) == ledgerSize {
		l.events = append(l.events[:0], l.events[1:]...)
	}
	l.events = append(l.events, event)
}

// after returns the events numbered after seq that the ledger still keeps.
func (l *ledger) after(seq int64) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	events := []Event{}
	for _, event := range l.events {
		if event.Seq > seq {
			events = append(events, event)
		}
	}
	return events
}
