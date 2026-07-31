// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestReadEntries(t *testing.T) {
	t.Run("InvalidDirectory", func(t *testing.T) {
		manager := scheduler.NewEntryReader("invalid_directory", nil, false)
		err := manager.Init(context.Background())
		require.Error(t, err)
	})
	t.Run("InitAndDAGs", func(t *testing.T) {
		th := test.SetupScheduler(t)
		ctx := context.Background()

		err := th.EntryReader.Init(ctx)
		require.NoError(t, err)

		dags := th.EntryReader.DAGs()
		require.NotEmpty(t, dags, "DAGs should not be empty")
	})
}
