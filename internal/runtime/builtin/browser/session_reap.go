// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
)

const (
	// reapPoll is the longest the watchdog of a session waits before
	// looking at the session again, so it exits soon after the session is
	// closed or ended elsewhere.
	reapPoll = 10 * time.Second
	// reapMargin lets a deadline pass before the watchdog looks at it.
	reapMargin = 100 * time.Millisecond
	// gateReapPoll is how often the watchdog looks at a session whose
	// browser has a write gate, whose loss leaves the browser's writes
	// undecided until it is closed.
	gateReapPoll = time.Second
)

// Reap watches the session id and closes its browser once the session is
// idle past its deadline, or its write gate is gone, keeping its history,
// unless a command renewed it meanwhile. It returns once the session has ended or been closed, so the
// browser of a session nobody closes does not outlive its idle timeout even
// with no server running.
func (s *Sessions) Reap(ctx context.Context, id string) error {
	if !sessionIDPattern.MatchString(id) {
		return sessionNotFound(id)
	}
	_, store, err := sessionDirs(ctx)
	if err != nil {
		return err
	}
	failed := false
	for {
		record, err := store.Load(id)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil
		case err != nil:
			return err
		case record.State == browserhost.StateEnded:
			return nil
		}
		wait := reapPoll
		if record.WriteGatePID != 0 {
			wait = gateReapPoll
		}
		// A browser that would not close is tried again a full poll later.
		if !failed && record.State == browserhost.StateInteractive && !record.Deadline.IsZero() {
			wait = min(wait, max(record.Deadline.Sub(s.now()), 0)+reapMargin)
		}
		if err := s.wait(ctx, wait); err != nil {
			return err
		}
		_, err = browserhost.RetireAbandoned(ctx, store, id, s.now())
		failed = err != nil
	}
}
