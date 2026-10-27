// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestArtifactKeySeparatesForeachItems(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "book", artifactKey("book", nil), "outside foreach the directory keeps its name")
	assert.Equal(t, "book-item-0", artifactKey("book", []int{0}))
	assert.Equal(t, "book-item-1", artifactKey("book", []int{1}))
	assert.NotEqual(t, artifactKey("book", []int{0, 1}), artifactKey("book", []int{1, 1}),
		"the same inner item under different outer items must not share a directory")
}
