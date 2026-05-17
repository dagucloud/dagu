// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workspacebundle

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStoreRejectsEmptyDir(t *testing.T) {
	t.Parallel()

	store := NewStore(" \t ", DefaultLimits())

	err := store.Put(context.Background(), Descriptor{}, nil)
	assert.ErrorContains(t, err, "workspace bundle store is not configured")
}
