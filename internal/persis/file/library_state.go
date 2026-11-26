// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"encoding/json"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
)

// scopedStateStore keeps each state scope in the store of what namespaces
// it: scope dag in the step's DAG's, scope root_dag in the root run's DAG's,
// and global and custom state in the project's.
type scopedStateStore struct {
	dag     dagrun.StateStore
	rootDAG dagrun.StateStore
	other   dagrun.StateStore
}

var _ dagrun.StateStore = scopedStateStore{}

func (s scopedStateStore) Get(ctx context.Context, ref dagrun.StateRef) (*dagrun.StateEntry, error) {
	return s.of(ref.Scope).Get(ctx, ref)
}

func (s scopedStateStore) Put(
	ctx context.Context,
	ref dagrun.StateRef,
	value json.RawMessage,
	opts dagrun.StatePutOptions,
) (*dagrun.StateEntry, error) {
	return s.of(ref.Scope).Put(ctx, ref, value, opts)
}

func (s scopedStateStore) Delete(ctx context.Context, ref dagrun.StateRef) (bool, error) {
	return s.of(ref.Scope).Delete(ctx, ref)
}

func (s scopedStateStore) List(ctx context.Context, opts dagrun.StateListOptions) ([]*dagrun.StateEntry, error) {
	return s.of(opts.Scope).List(ctx, opts)
}

func (s scopedStateStore) of(scope dagrun.StateScope) dagrun.StateStore {
	switch scope {
	case dagrun.StateScopeDAG:
		return s.dag
	case dagrun.StateScopeRootDAG:
		return s.rootDAG
	default:
		return s.other
	}
}
