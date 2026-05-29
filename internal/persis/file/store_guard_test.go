// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/file/proc"
	filesession "github.com/dagucloud/dagu/internal/persis/file/session"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/stretchr/testify/require"
)

// TestFileBackendUsesFileSpecificStores locks the invariant that the file
// backend wires proc and agent-session through the layout-preserving,
// file-specific implementations — never the backend-neutral collection
// adapters in internal/persis/store.
//
// The collection-backed store.ProcStore (JSON records) and store.SessionStore
// (flat "{sessionID}.json") are NOT layout-compatible with the released
// file-backend layouts (binary ".proc" files; nested "{userID}/{sessionID}.json").
// Swapping the wiring to those would silently break upgrades, so guard it here.
//
// NewProcStore returns a concrete *proc.Store, so the compiler already prevents
// a proc swap; this test documents that and additionally guards the session
// boundary, whose constructor returns the agent.SessionStore interface and so
// would accept a store.SessionStore swap without a compile error.
func TestFileBackendUsesFileSpecificStores(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Paths.ProcDir = t.TempDir()
	cfg.Paths.SessionsDir = t.TempDir()

	t.Run("proc store is file-backed", func(t *testing.T) {
		t.Parallel()

		ps := file.NewProcStore(cfg)
		require.IsType(t, &proc.Store{}, ps,
			"file backend must use the binary .proc store, not a collection adapter")

		var asCollectionStore any = ps
		_, isCollectionBacked := asCollectionStore.(*store.ProcStore)
		require.False(t, isCollectionBacked,
			"file backend proc store must not be the collection-backed store.ProcStore")
	})

	t.Run("agent session store is file-backed", func(t *testing.T) {
		t.Parallel()

		ss, err := file.NewAgentSessionStore(cfg)
		require.NoError(t, err)
		require.IsType(t, &filesession.Store{}, ss,
			"file backend must use the nested filesession store, not a collection adapter")

		_, isCollectionBacked := ss.(*store.SessionStore)
		require.False(t, isCollectionBacked,
			"file backend agent session store must not be the collection-backed store.SessionStore")
	})
}
