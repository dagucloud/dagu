// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/require"
)

func TestNewDependenciesRequiresDAGSettingsStorage(t *testing.T) {
	t.Parallel()

	_, err := NewDependencies(t.Context(), &config.Config{})

	require.ErrorContains(t, err, "failed to initialize DAG settings store")
}
