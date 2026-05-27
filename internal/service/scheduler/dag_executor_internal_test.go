// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/stretchr/testify/require"
)

func TestBuildSnapshotBuilder_AllowsNilDAGStore(t *testing.T) {
	t.Parallel()

	builder := buildSnapshotBuilder(config.PathsConfig{}, nil, nil)
	require.NotNil(t, builder)
}
