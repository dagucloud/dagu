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
}

// lastCallInterval is how long calls from the same app and person go unsaved
// after one is saved; the time is shown no more precisely than that.
const lastCallInterval = time.Minute

// lastCalls keeps the latest call in a store, so it outlives a restart.
type lastCalls struct {
	store StateStore
	now   func() time.Time

	mu    sync.Mutex
	saved LastCall
}

// note records a call from client as user, unless one from the same app and
// person was saved within lastCallInterval.
func (l *lastCalls) note(ctx context.Context, client, user string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.saved.Client == client && l.saved.User == user && now.Sub(l.saved.At) < lastCallInterval {
		return
	}
	call := LastCall{At: now, Client: client, User: user}
	data, err := json.Marshal(call)
	if err == nil {
		err = l.store.Save(ctx, data)
	}
	if err != nil {
		logger.Warn(ctx, "Remote access: the last call could not be saved", tag.Error(err))
		return
	}
	l.saved = call
}

// get returns the latest call saved, and false while there is none.
func (l *lastCalls) get(ctx context.Context) (LastCall, bool, error) {
	data, found, err := l.store.Load(ctx)
	if err != nil || !found {
		return LastCall{}, false, err
	}
	var call LastCall
	if err := json.Unmarshal(data, &call); err != nil {
		return LastCall{}, false, fmt.Errorf("read the last call from Dagu Cloud: %w", err)
	}
	return call, !call.At.IsZero(), nil
}
