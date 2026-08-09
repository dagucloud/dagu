// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagstate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeValueRejectsNormalizedValueOverLimit(t *testing.T) {
	raw := []byte(`"` + strings.Repeat("<", DefaultMaxValueBytes/6+1) + `"`)
	assert.Less(t, len(raw), DefaultMaxValueBytes)

	_, err := NormalizeValue(raw)
	require.ErrorIs(t, err, ErrValueTooLarge)
}

func TestNormalizeValuePreservesNumericPrecision(t *testing.T) {
	value, err := NormalizeValue([]byte(`{"id":9007199254740993,"decimal":1.2300}`))
	require.NoError(t, err)
	assert.Equal(t, `{"decimal":1.2300,"id":9007199254740993}`, string(value))
}
