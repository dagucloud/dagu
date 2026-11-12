// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

// LastCall is the latest call from Dagu Cloud the server served: when, the
// app that made it, and the person it ran as.
type LastCall struct {
	At     time.Time `json:"at"`
	Client string    `json:"client,omitempty"`
	User   string    `json:"user,omitempty"`
	// License is the license the server served the call under; a call
	// under another license, as before the server moved to another
	// workspace, is not shown.
	License string `json:"license"`
}

// lastCallInterval is how long calls from the same app and person go unsaved
// after one is saved; the time is shown no more precisely than that.
const lastCallInterval = time.Minute

// lastCalls keeps the latest call in a store, so it outlives a restart.
type lastCalls struct {
	store StateStore
	now   func() time.Time

	mu     sync.Mutex
	latest LastCall
	// noted counts the calls noted, so a save can tell whether its call is
	// still the latest.
	noted uint64
	// saving serializes saves, each of which writes only the latest call.
	saving sync.Mutex
}

// note records a call from client as user under license, unless one from
// the same app and person was noted within lastCallInterval. The call is
// saved in the background, off the call's path.
func (l *lastCalls) note(ctx context.Context, license, client, user string) {
	l.mu.Lock()
	now := l.now()
	last := l.latest
	if last.License == license && last.Client == client && last.User == user && now.Sub(last.At) < lastCallInterval {
		l.mu.Unlock()
		return
	}
	call := LastCall{At: now, Client: client, User: user, License: license}
	l.latest = call
	l.noted++
	n := l.noted
	l.mu.Unlock()
	go l.save(context.WithoutCancel(ctx), call, n)
}

// save writes call, the nth noted, unless a later one was noted, which is
// written instead.
func (l *lastCalls) save(ctx context.Context, call LastCall, n uint64) {
	l.saving.Lock()
	defer l.saving.Unlock()
	l.mu.Lock()
	later := l.noted != n
	l.mu.Unlock()
	if later {
		return
	}
	data, err := json.Marshal(call)
	if err == nil {
		err = l.store.Save(ctx, data)
	}
	if err != nil {
		logger.Warn(ctx, "Remote access: the last call could not be saved", tag.Error(err))
	}
}

// get returns the latest call saved under license, and false while there is
// none.
func (l *lastCalls) get(ctx context.Context, license string) (LastCall, bool, error) {
	data, found, err := l.store.Load(ctx)
	if err != nil || !found {
		return LastCall{}, false, err
	}
	var call LastCall
	if err := json.Unmarshal(data, &call); err != nil {
		return LastCall{}, false, fmt.Errorf("read the last call from Dagu Cloud: %w", err)
	}
	return call, !call.At.IsZero() && call.License == license, nil
}
