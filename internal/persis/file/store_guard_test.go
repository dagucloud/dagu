// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/file/proc"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/stretchr/testify/require"
)

// TestFileBackendUsesFileSpecificProcStore locks the invariant that the file
// backend wires proc through the binary ".proc" implementation, not the
// collection-backed store.ProcStore. The proc layout uses a custom file
// extension that Collection (which forces ".json") cannot represent, so
// swapping the wiring would silently break upgrades.
//
// NewProcStore returns a concrete *proc.Store, so the compiler already
// prevents a swap; this test documents the contract for readers.
func TestFileBackendUsesFileSpecificProcStore(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Paths.ProcDir = t.TempDir()

	ps := file.NewProcStore(cfg)
	require.IsType(t, &proc.Store{}, ps,
		"file backend must use the binary .proc store, not a collection adapter")

	var asCollectionStore any = ps
	_, isCollectionBacked := asCollectionStore.(*store.ProcStore)
	require.False(t, isCollectionBacked,
		"file backend proc store must not be the collection-backed store.ProcStore")
}
