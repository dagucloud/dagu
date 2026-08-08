// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/stretchr/testify/require"
)

func TestNewDAGRunID(t *testing.T) {
	t.Parallel()

	id, err := dagrun.NewDAGRunID()
	require.NoError(t, err)
	require.Regexp(t, `^[0-9A-Za-z]{22}$`, id)
	require.NoError(t, dagrun.ValidateDAGRunID(id))
}
