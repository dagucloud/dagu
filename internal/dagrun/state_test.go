// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeStateValueCompactsJSON(t *testing.T) {
	value, err := dagrun.NormalizeStateValue([]byte(`{ "b": 2, "a": 1 }`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"b":2}`, string(value))

	_, err = dagrun.NormalizeStateValue([]byte(`{`))
	require.ErrorIs(t, err, dagrun.ErrInvalidStateValue)
}

func TestNormalizeStateValueRejectsNormalizedValueOverLimit(t *testing.T) {
	raw := []byte(`"` + strings.Repeat("<", dagrun.MaxStateValueBytes/6+1) + `"`)
	assert.Less(t, len(raw), dagrun.MaxStateValueBytes)

	_, err := dagrun.NormalizeStateValue(raw)
	require.ErrorIs(t, err, dagrun.ErrStateValueTooLarge)
}

func TestNormalizeStateValuePreservesNumericPrecision(t *testing.T) {
	value, err := dagrun.NormalizeStateValue([]byte(`{"id":9007199254740993,"decimal":1.2300}`))
	require.NoError(t, err)
	assert.Equal(t, `{"decimal":1.2300,"id":9007199254740993}`, string(value))
}
