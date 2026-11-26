// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validSkill = "---\nname: refund-policy\ndescription: Rules to refund an order. Use when a customer asks for a refund.\n---\n# Refunds\nRefund in 30 days.\n"

func skillsDir(server test.Server) string {
	return filepath.Join(server.Config.Paths.DAGsDir, "skills")
}

// writeSkillFile creates a skill folder directly on disk, as git or a person would.
func writeSkillFile(t *testing.T, server test.Server, name, content string) {
	t.Helper()
	folder := filepath.Join(skillsDir(server), name)
	require.NoError(t, os.MkdirAll(folder, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte(content), 0o600))
}

func issueRules(issues []api.SkillIssue) []string {
	var out []string
	for _, i := range issues {
		out = append(out, i.Rule)
	}
	return out
}

func TestSkillsList(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)

	list := func() []api.SkillSummary {
		var out api.ListSkills200JSONResponse
		server.Client().Get("/api/v1/skills").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &out)
		return out.Skills
	}

	t.Run("MissingDirectory", func(t *testing.T) {
		assert.Empty(t, list())
	})

	t.Run("SortedWithInvalid", func(t *testing.T) {
		writeSkillFile(t, server, "zeta", "---\nname: zeta\ndescription: Last\n---\nBody\n")
		writeSkillFile(t, server, "refund-policy", validSkill)
		writeSkillFile(t, server, "broken", "no front matter\n")
		require.NoError(t, os.MkdirAll(filepath.Join(skillsDir(server), "empty"), 0o750))
		require.NoError(t, os.MkdirAll(filepath.Join(skillsDir(server), ".hidden"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(skillsDir(server), "README.md"), []byte("x"), 0o600))

		got := list()
		require.Len(t, got, 4, "a folder with errors is listed, a plain file and a hidden folder are not")
		assert.Equal(t, "broken", got[0].Name)
		assert.False(t, got[0].Valid)
		require.NotEmpty(t, got[0].Errors)
		assert.Equal(t, "front-matter-start", got[0].Errors[0].Rule)
		require.NotNil(t, got[0].Errors[0].File)
		assert.Equal(t, "SKILL.md", *got[0].Errors[0].File)
		assert.Equal(t, "empty", got[1].Name)
		assert.Contains(t, issueRules(got[1].Errors), "skill-md-missing")
		assert.Equal(t, "refund-policy", got[2].Name)
		assert.True(t, got[2].Valid)
		assert.Empty(t, got[2].Errors)
		assert.Len(t, got[2].Hash, 64)
		assert.Equal(t, 1, got[2].FileCount)
		assert.Equal(t, 2, got[2].BodyLines)
		assert.Greater(t, got[2].BodyTokens, 0)
		assert.Equal(t, "zeta", got[3].Name)
		assert.True(t, got[3].Valid, "a short description is a warning, not an error")
		assert.Contains(t, issueRules(got[3].Warnings), "description-short")
	})
}

// A listing reads at most skill.MaxListedSkills folders, the first in name
// order, and says when the directory holds more.
func TestSkillsListIsBounded(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	for i := range skill.MaxListedSkills + 1 {
		require.NoError(t, os.MkdirAll(filepath.Join(skillsDir(server), fmt.Sprintf("s%04d", i)), 0o750))
	}
	list := func() api.ListSkills200JSONResponse {
		var out api.ListSkills200JSONResponse
		server.Client().Get("/api/v1/skills").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &out)
		return out
	}

	got := list()
	require.Len(t, got.Skills, skill.MaxListedSkills)
	assert.True(t, got.Truncated)
	assert.Equal(t, "s0000", got.Skills[0].Name)

	require.NoError(t, os.Remove(filepath.Join(skillsDir(server), "s0000")))
	got = list()
	require.Len(t, got.Skills, skill.MaxListedSkills)
	assert.False(t, got.Truncated)
}
