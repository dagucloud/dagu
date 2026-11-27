// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/stretchr/testify/require"
)

func TestNewDependenciesRequiresDAGSettingsStorage(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	server := persisfile.NewServerStores(cfg)
	settings, err := persisfile.NewProjectSettings(cfg, server, project.DefaultName)
	require.NoError(t, err)
	_, err = NewDependencies(t.Context(), cfg, server, settings, 0)

	require.ErrorContains(t, err, "failed to initialize DAG settings store")
}
