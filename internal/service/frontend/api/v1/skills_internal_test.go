// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A conflict with no known version, such as the end of the wait for another
// writer, has no hash in its body, so a client never retries with an empty
// If-Match.
func TestSkillConflictHash(t *testing.T) {
	t.Parallel()
	a := &API{}

	known := a.classifySkillWrite(context.Background(), "refund-policy", &skill.ConflictError{Current: "abc", Message: "changed"})
	assert.Equal(t, http.StatusConflict, known.status)
	require.NotNil(t, known.body.Hash)
	assert.Equal(t, "abc", *known.body.Hash)

	unknown := a.classifySkillWrite(context.Background(), "refund-policy", &skill.ConflictError{Message: "another writer holds the skill"})
	assert.Equal(t, http.StatusConflict, unknown.status)
	assert.Nil(t, unknown.body.Hash)
}
