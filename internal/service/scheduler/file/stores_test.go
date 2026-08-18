// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/stretchr/testify/require"
)

func TestNewDependenciesRequiresDAGSettingsStorage(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	_, err := NewDependencies(t.Context(), cfg, persisfile.NewBackend(cfg.Paths))

	require.ErrorContains(t, err, "failed to initialize DAG settings store")
}
