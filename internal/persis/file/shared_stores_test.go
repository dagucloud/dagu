// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
)

func TestNewEventCollectorDisabledWhenConfigNilOrEventStoreDisabled(t *testing.T) {
	t.Parallel()

	collector, err := file.NewEventCollector(nil)
	require.NoError(t, err)
	assert.Nil(t, collector)

	collector, err = file.NewEventCollector(&config.Config{})
	require.NoError(t, err)
	assert.Nil(t, collector)
}
