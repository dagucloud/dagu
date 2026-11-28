// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/service/worker"
)

// fakeRemoval is a membership whose client met a refusal as revoked when
// revoked is closed.
type fakeRemoval struct {
	revoked chan struct{}
	removed int
}

func (f *fakeRemoval) Revoked() <-chan struct{} { return f.revoked }

func (f *fakeRemoval) Removed(context.Context) error {
	f.removed++
	return errors.New("This worker was removed from the coordinator")
}

// A removal decides the exit however the worker ended, a signal included,
// so it records the removal and exits with its message (Spec 108 §Removed
// Worker).
func TestWorkerExit(t *testing.T) {
	t.Parallel()
	stopFailed := errors.New("failed to stop worker")
	closed := make(chan struct{})
	close(closed)
	for _, tc := range []struct {
		name        string
		revoked     chan struct{}
		err         error
		wantRemoved bool
		wantErr     error
	}{
		{name: "SignalAfterRemoval", revoked: closed, wantRemoved: true},
		{name: "StopFailedAfterRemoval", revoked: closed, err: stopFailed, wantRemoved: true},
		{name: "StartReturnedRemoved", revoked: make(chan struct{}), err: worker.ErrRemoved, wantRemoved: true},
		{name: "Signal", revoked: make(chan struct{})},
		{name: "StopFailed", revoked: make(chan struct{}), err: stopFailed, wantErr: stopFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			membership := &fakeRemoval{revoked: tc.revoked}
			err := workerExit(t.Context(), membership, tc.err)
			if tc.wantRemoved {
				assert.EqualError(t, err, "This worker was removed from the coordinator")
				assert.Equal(t, 1, membership.removed)
				return
			}
			assert.Equal(t, tc.wantErr, err)
			assert.Zero(t, membership.removed)
		})
	}
}
