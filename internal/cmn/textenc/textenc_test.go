// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package textenc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/japanese"
)

func TestDetect(t *testing.T) {
	t.Parallel()
	sjis, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte("商品"))
	require.NoError(t, err)

	plain, err := Detect([]byte("商品"))
	require.NoError(t, err)
	assert.Equal(t, "商品", string(plain))

	marked, err := Detect([]byte(UTF8BOM + "商品"))
	require.NoError(t, err)
	assert.Equal(t, "商品", string(marked), "the byte order mark is dropped")

	decoded, err := Detect(sjis)
	require.NoError(t, err)
	assert.Equal(t, "商品", string(decoded))

	_, err = Detect(append([]byte(UTF8BOM), sjis...))
	assert.ErrorIs(t, err, ErrNotUTF8, "a byte order mark settles the encoding")

	_, err = Detect([]byte{0xFF, 0xFE, 'a'})
	assert.ErrorIs(t, err, ErrUnknownEncoding)
}
