// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func TestMemoryCollection(t *testing.T) {
	t.Parallel()

	factory := func(t *testing.T) (persis.Collection, persis.Collection) {
		t.Helper()
		backend := testutil.NewMemoryBackend()
		return backend.Collection("test"), backend.Collection("test")
	}
	testutil.RunCollectionContract(t, factory)
	testutil.RunLockingCollectionContract(t, factory)
}
