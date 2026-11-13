// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
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

func getSkill(t *testing.T, server test.Server, name string) api.SkillDetail {
	t.Helper()
	var out api.GetSkill200JSONResponse
	server.Client().Get("/api/v1/skills/"+name).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &out)
	return api.SkillDetail(out)
}

func ifMatch(hash string) map[string]string { return map[string]string{"If-Match": hash} }

func put(server test.Server, path string, body any, headers map[string]string) *test.Request {
	r := server.Client().Put(path, body)
	for k, v := range headers {
		r = r.WithHeader(k, v)
	}
	return r
}

func del(server test.Server, path string, headers map[string]string) *test.Request {
	r := server.Client().Delete(path)
	for k, v := range headers {
		r = r.WithHeader(k, v)
	}
	return r
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
		require.NoError(t, os.WriteFile(filepath.Join(skillsDir(server), "README.md"), []byte("x"), 0o600))

		got := list()
		require.Len(t, got, 4, "a folder with errors is listed, a plain file is not")
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

func TestSkillsGet(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	writeSkillFile(t, server, "refund-policy", validSkill)
	writeSkillFile(t, server, "broken", "---\nname: other\ndescription: d\nmodel: x\n---\nBody\n")
	require.NoError(t, os.MkdirAll(filepath.Join(skillsDir(server), "refund-policy", "references"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir(server), "refund-policy", "references", "faq.md"), []byte("# FAQ\n"), 0o600))

	t.Run("Valid", func(t *testing.T) {
		out := getSkill(t, server, "refund-policy")
		assert.Equal(t, "refund-policy", out.Name)
		assert.Equal(t, validSkill, out.Content)
		assert.Empty(t, out.Errors)
		assert.Empty(t, out.Warnings)
		assert.Len(t, out.Hash, 64)
		assert.Equal(t, out.Hash, out.Summary.Hash)
		require.Len(t, out.Files, 2)
		assert.Equal(t, "SKILL.md", out.Files[0].Path)
		assert.Equal(t, "references/faq.md", out.Files[1].Path)
		assert.True(t, out.Files[1].Text)
	})

	t.Run("Invalid", func(t *testing.T) {
		out := getSkill(t, server, "broken")
		assert.Contains(t, out.Content, "name: other")
		assert.ElementsMatch(t, []string{"front-matter-key", "name-folder"}, issueRules(out.Errors))
		assert.False(t, out.Summary.Valid)
	})

	t.Run("NotFound", func(t *testing.T) {
		server.Client().Get("/api/v1/skills/missing").ExpectStatus(http.StatusNotFound).Send(t)
	})

	t.Run("UnsafeName", func(t *testing.T) {
		server.Client().Get("/api/v1/skills/.hidden").ExpectStatus(http.StatusBadRequest).Send(t)
		server.Client().Get("/api/v1/skills/a%5Cb").ExpectStatus(http.StatusBadRequest).Send(t)
	})
}

func TestSkillsPutNeedsAPrecondition(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	body := api.UpdateSkillJSONRequestBody{Content: validSkill}

	put(server, "/api/v1/skills/refund-policy", body, nil).ExpectStatus(http.StatusPreconditionRequired).Send(t)
	put(server, "/api/v1/skills/refund-policy", body, ifMatch("nope")).ExpectStatus(http.StatusBadRequest).Send(t)
	_, err := os.Stat(filepath.Join(skillsDir(server), "refund-policy"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestSkillsPutChainAndConflict(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	create := map[string]string{"If-None-Match": "*"}
	file := filepath.Join(skillsDir(server), "refund-policy", "SKILL.md")

	var created api.UpdateSkill200JSONResponse
	put(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: validSkill}, create).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &created)
	assert.Len(t, created.Hash, 64)
	assert.Empty(t, created.Errors)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, validSkill, string(data))
	assert.Equal(t, created.Hash, getSkill(t, server, "refund-policy").Hash)

	t.Run("CreateOfAnExistingSkillConflicts", func(t *testing.T) {
		var out api.SkillError
		put(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: validSkill}, create).
			ExpectStatus(http.StatusConflict).Send(t).Unmarshal(t, &out)
		assert.Equal(t, api.ErrorCodeConflict, out.Code)
		require.NotNil(t, out.Hash)
		assert.Equal(t, created.Hash, *out.Hash)
	})

	next := "---\nname: refund-policy\ndescription: Changed rules to refund an order.\n---\nNew body\n"
	var updated api.UpdateSkill200JSONResponse
	t.Run("OverwriteWithTheHash", func(t *testing.T) {
		put(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: next}, ifMatch(created.Hash)).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &updated)
		assert.NotEqual(t, created.Hash, updated.Hash)
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Equal(t, next, string(data))
	})

	t.Run("StaleHashConflictsAndKeepsTheFile", func(t *testing.T) {
		var out api.SkillError
		put(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: validSkill}, ifMatch(created.Hash)).
			ExpectStatus(http.StatusConflict).Send(t).Unmarshal(t, &out)
		require.NotNil(t, out.Hash)
		assert.Equal(t, updated.Hash, *out.Hash, "the conflict gives the current hash")
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Equal(t, next, string(data))
	})

	t.Run("AChangeOnDiskIsAConflict", func(t *testing.T) {
		require.NoError(t, os.WriteFile(file, []byte(strings.Replace(next, "New body", "Edited by hand", 1)), 0o600))
		put(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: next}, ifMatch(updated.Hash)).
			ExpectStatus(http.StatusConflict).Send(t)
	})

	t.Run("InvalidContentGivesTheIssues", func(t *testing.T) {
		cur := getSkill(t, server, "refund-policy").Hash
		for content, rule := range map[string]string{
			"---\nname: other\ndescription: Some description of the skill.\n---\nBody\n":      "name-folder",
			"---\nname: refund-policy\ndescription: Some description.\nmodel: x\n---\nBody\n": "front-matter-key",
			"just text": "front-matter-start",
		} {
			var out api.SkillError
			put(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: content}, ifMatch(cur)).
				ExpectStatus(http.StatusBadRequest).Send(t).Unmarshal(t, &out)
			assert.Equal(t, api.ErrorCodeBadRequest, out.Code)
			require.NotNil(t, out.Issues)
			assert.Contains(t, issueRules(*out.Issues), rule)
		}
		assert.Equal(t, cur, getSkill(t, server, "refund-policy").Hash)
	})

	t.Run("InvalidName", func(t *testing.T) {
		for _, name := range []string{"Bad", "9-"} {
			body := api.UpdateSkillJSONRequestBody{Content: strings.ReplaceAll(validSkill, "refund-policy", name)}
			put(server, "/api/v1/skills/"+name, body, create).ExpectStatus(http.StatusBadRequest).Send(t)
		}
	})
}

func TestSkillsFiles(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	var cur api.UpdateSkill200JSONResponse
	put(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: validSkill}, map[string]string{"If-None-Match": "*"}).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &cur)

	putFile := func(path string, data []byte, hash string, status int) api.SkillWriteResult {
		var out api.SkillWriteResult
		resp := put(server, "/api/v1/skills/refund-policy/files?path="+path, data, ifMatch(hash)).ExpectStatus(status).Send(t)
		if status == http.StatusOK {
			resp.Unmarshal(t, &out)
		}
		return out
	}

	t.Run("WriteReadDeleteChain", func(t *testing.T) {
		a := putFile("references/faq.md", []byte("# FAQ\n"), cur.Hash, http.StatusOK)
		assert.NotEqual(t, cur.Hash, a.Hash)
		require.Len(t, a.Files, 2)
		b := putFile("assets/logo.bin", []byte{0, 1, 2, 255}, a.Hash, http.StatusOK)
		require.Len(t, b.Files, 3)
		assert.False(t, b.Files[1].Text)

		resp := server.Client().Get("/api/v1/skills/refund-policy/files?path=assets/logo.bin").ExpectStatus(http.StatusOK).Send(t)
		assert.Equal(t, []byte{0, 1, 2, 255}, resp.Response.Body())
		assert.Equal(t, `"`+b.Hash+`"`, resp.Response.Header().Get("ETag"))
		resp = server.Client().Get("/api/v1/skills/refund-policy/files?path=references/faq.md").ExpectStatus(http.StatusOK).Send(t)
		assert.Equal(t, "# FAQ\n", resp.Body)

		// The old hash conflicts.
		putFile("references/other.md", []byte("x"), a.Hash, http.StatusConflict)

		var removed api.SkillWriteResult
		del(server, "/api/v1/skills/refund-policy/files?path=assets/logo.bin", ifMatch(b.Hash)).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &removed)
		assert.Equal(t, a.Hash, removed.Hash)
		_, err := os.Stat(filepath.Join(skillsDir(server), "refund-policy", "assets", "logo.bin"))
		assert.ErrorIs(t, err, os.ErrNotExist)
		cur.Hash = removed.Hash
	})

	t.Run("RulesOfTheFolderApply", func(t *testing.T) {
		h := getSkill(t, server, "refund-policy").Hash
		putFile("README.md", []byte("x"), h, http.StatusBadRequest)
		putFile("docs/a.md", []byte("x"), h, http.StatusBadRequest)
		putFile("references/.env", []byte("x"), h, http.StatusBadRequest)
		putFile("references/bad.md", []byte{0xff, 0xfe}, h, http.StatusBadRequest)
		putFile("SKILL.md", []byte(validSkill), h, http.StatusBadRequest)
		putFile("references/..%2F..%2Fevil.md", []byte("x"), h, http.StatusBadRequest)
		putFile("%2Fetc%2Fevil", []byte("x"), h, http.StatusBadRequest)
		putFile("references/big.md", bytes.Repeat([]byte("x"), 4<<20+1), h, http.StatusRequestEntityTooLarge)
		put(server, "/api/v1/skills/refund-policy/files?path=references/a.md", []byte("x"), nil).
			ExpectStatus(http.StatusPreconditionRequired).Send(t)
		put(server, "/api/v1/skills/missing/files?path=references/a.md", []byte("x"), ifMatch(h)).
			ExpectStatus(http.StatusNotFound).Send(t)
		assert.Equal(t, h, getSkill(t, server, "refund-policy").Hash, "a refused write changes nothing")
	})

	t.Run("ReadAnUnknownFileOrAnEscape", func(t *testing.T) {
		server.Client().Get("/api/v1/skills/refund-policy/files?path=references/none.md").ExpectStatus(http.StatusNotFound).Send(t)
		server.Client().Get("/api/v1/skills/refund-policy/files?path=..%2F..%2Fx").ExpectStatus(http.StatusBadRequest).Send(t)
		server.Client().Get("/api/v1/skills/refund-policy/files?path=%2Fetc%2Fpasswd").ExpectStatus(http.StatusBadRequest).Send(t)
	})

	t.Run("AnInvalidFileCanBeDeleted", func(t *testing.T) {
		h := getSkill(t, server, "refund-policy").Hash
		stray := filepath.Join(skillsDir(server), "refund-policy", "notes.txt")
		require.NoError(t, os.WriteFile(stray, []byte("x"), 0o600))
		detail := getSkill(t, server, "refund-policy")
		assert.Contains(t, issueRules(detail.Errors), "root-entry")
		var out api.SkillWriteResult
		del(server, "/api/v1/skills/refund-policy/files?path=notes.txt", ifMatch(detail.Hash)).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &out)
		assert.Empty(t, out.Errors)
		assert.Equal(t, h, out.Hash)
	})
}

func TestSkillsDelete(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	writeSkillFile(t, server, "refund-policy", validSkill)
	extra := filepath.Join(skillsDir(server), "refund-policy", "references")
	require.NoError(t, os.MkdirAll(extra, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(extra, "a.md"), []byte("x"), 0o600))
	h := getSkill(t, server, "refund-policy").Hash

	del(server, "/api/v1/skills/refund-policy", nil).ExpectStatus(http.StatusPreconditionRequired).Send(t)
	del(server, "/api/v1/skills/refund-policy", ifMatch(strings.Repeat("0", 64))).ExpectStatus(http.StatusConflict).Send(t)
	del(server, "/api/v1/skills/refund-policy", ifMatch(h)).ExpectStatus(http.StatusNoContent).Send(t)
	_, err := os.Stat(filepath.Join(skillsDir(server), "refund-policy"))
	assert.ErrorIs(t, err, os.ErrNotExist, "the whole folder goes")
	del(server, "/api/v1/skills/refund-policy", ifMatch(h)).ExpectStatus(http.StatusNotFound).Send(t)
	del(server, "/api/v1/skills/.x", ifMatch(h)).ExpectStatus(http.StatusBadRequest).Send(t)
}

func unzip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		_ = rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestSkillsDownload(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	writeSkillFile(t, server, "alpha", "---\nname: alpha\ndescription: Alpha rules. Use when asked.\n---\nA\n")
	writeSkillFile(t, server, "beta", "---\nname: beta\ndescription: Beta rules. Use when asked.\n---\nB\n")
	writeSkillFile(t, server, "broken", "nope\n")
	require.NoError(t, os.MkdirAll(filepath.Join(skillsDir(server), "alpha", "assets"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir(server), "alpha", "assets", "x.bin"), []byte{0, 255}, 0o600))

	t.Run("OneSkill", func(t *testing.T) {
		resp := server.Client().Get("/api/v1/skills/alpha/download").ExpectStatus(http.StatusOK).Send(t)
		assert.Equal(t, "application/zip", resp.Response.Header().Get("Content-Type"))
		assert.Equal(t, `attachment; filename="alpha.zip"`, resp.Response.Header().Get("Content-Disposition"))
		files := unzip(t, resp.Response.Body())
		assert.Equal(t, map[string]string{
			"alpha/SKILL.md":     "---\nname: alpha\ndescription: Alpha rules. Use when asked.\n---\nA\n",
			"alpha/assets/x.bin": "\x00\xff",
		}, files)
	})

	t.Run("SeveralSkillsAreDeterministic", func(t *testing.T) {
		post := func(names ...string) []byte {
			resp := server.Client().Post("/api/v1/skills/download", map[string]any{"names": names}).ExpectStatus(http.StatusOK).Send(t)
			assert.Equal(t, "application/zip", resp.Response.Header().Get("Content-Type"))
			assert.Regexp(t, `^attachment; filename="skills-\d{4}-\d{2}-\d{2}\.zip"$`, resp.Response.Header().Get("Content-Disposition"))
			return resp.Response.Body()
		}
		first := post("beta", "alpha", "broken")
		second := post("alpha", "broken", "beta", "alpha")
		assert.Equal(t, first, second)
		files := unzip(t, first)
		assert.Len(t, files, 4)
		assert.Equal(t, "nope\n", files["broken/SKILL.md"], "an invalid skill can be downloaded, marked nowhere")
		assert.Equal(t, "---\nname: beta\ndescription: Beta rules. Use when asked.\n---\nB\n", files["beta/SKILL.md"])
	})

	t.Run("UnknownNameGivesNoZip", func(t *testing.T) {
		var out api.Error
		resp := server.Client().Post("/api/v1/skills/download", map[string]any{"names": []string{"alpha", "nope"}}).
			ExpectStatus(http.StatusNotFound).Send(t)
		resp.Unmarshal(t, &out)
		assert.Contains(t, out.Message, "nope")
		assert.Equal(t, "application/json", resp.Response.Header().Get("Content-Type"))
		server.Client().Get("/api/v1/skills/nope/download").ExpectStatus(http.StatusNotFound).Send(t)
	})

	t.Run("Limits", func(t *testing.T) {
		names := make([]string, 101)
		for i := range names {
			names[i] = "s" + strings.Repeat("a", i+1)
		}
		server.Client().Post("/api/v1/skills/download", map[string]any{"names": names}).ExpectStatus(http.StatusBadRequest).Send(t)
		server.Client().Post("/api/v1/skills/download", map[string]any{"names": []string{}}).ExpectStatus(http.StatusBadRequest).Send(t)
	})
}

// A skill name that is a traversal never reaches a file outside the skills
// directory.
func TestSkillsPathTraversal(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	outside := filepath.Join(server.Config.Paths.DAGsDir, "SKILL.md")
	require.NoError(t, os.WriteFile(outside, []byte(validSkill), 0o600))

	for _, name := range []string{"..", "..%2F..", "%2e%2e", "a%2Fb"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			var req *test.Request
			switch method {
			case http.MethodGet:
				req = server.Client().Get("/api/v1/skills/" + name)
			case http.MethodPut:
				req = put(server, "/api/v1/skills/"+name, api.UpdateSkillJSONRequestBody{Content: validSkill}, map[string]string{"If-None-Match": "*"})
			default:
				req = del(server, "/api/v1/skills/"+name, ifMatch(strings.Repeat("0", 64)))
			}
			status := req.Send(t).Response.StatusCode()
			assert.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, status, "%s %s", method, name)
		}
	}

	_, err := os.Stat(outside)
	require.NoError(t, err, "file outside the skills directory must stay")
}

// A skill whose folder is a link, and a folder of a skill that is a link, are read with
// errors. A write through them is refused with 400 and the rule of the link, also with
// the hash that the client read. The folder outside the skills directory does not change.
func TestSkillsWriteRefusesSymbolicLinks(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(skillsDir(server), 0o750))

	// The folder of the skill is a link.
	require.NoError(t, os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte(validSkill), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(skillsDir(server), "linked")))
	linked := getSkill(t, server, "linked")
	assert.Contains(t, issueRules(linked.Errors), "folder-link")
	put(server, "/api/v1/skills/linked/files?path=references/a.md", []byte("x"), ifMatch(linked.Hash)).
		ExpectStatus(http.StatusBadRequest).Send(t)

	// A folder inside the skill is a link.
	writeSkillFile(t, server, "refund-policy", validSkill)
	require.NoError(t, os.Symlink(outside, filepath.Join(skillsDir(server), "refund-policy", "references")))
	detail := getSkill(t, server, "refund-policy")
	put(server, "/api/v1/skills/refund-policy/files?path=references/a.md", []byte("x"), ifMatch(detail.Hash)).
		ExpectStatus(http.StatusBadRequest).Send(t)

	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Len(t, entries, 1, "nothing is written outside the skills directory")
}

// Two API instances on one directory stand for two servers of one HA group.
// Both write with the hash that they read: one wins, the other gets a conflict.
func TestSkillsTwoInstancesRaceOnOneDirectory(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	newAPI := func() *localapi.API {
		return localapi.New(
			helper.DAGRepository, helper.DAGRunRepository, helper.QueueStore, helper.ProcRepository,
			helper.DAGRunMgr, helper.Config, nil, helper.ServiceRegistry, nil, nil,
		)
	}
	ctx := context.Background()
	none := "*"
	first := newAPI()
	created, err := first.UpdateSkill(ctx, api.UpdateSkillRequestObject{
		Name: "refund-policy", Params: api.UpdateSkillParams{IfNoneMatch: &none},
		Body: &api.UpdateSkillJSONRequestBody{Content: validSkill},
	})
	require.NoError(t, err)
	hash := created.(api.UpdateSkill200JSONResponse).Hash

	for round := range 10 {
		instances := []*localapi.API{newAPI(), newAPI()}
		contents := []string{
			strings.Replace(validSkill, "Refund in 30 days.", "Round "+strings.Repeat("a", round+1), 1),
			strings.Replace(validSkill, "Refund in 30 days.", "Round "+strings.Repeat("b", round+1), 1),
		}
		results := make([]api.UpdateSkillResponseObject, 2)
		var wg sync.WaitGroup
		for i := range instances {
			wg.Go(func() {
				h := hash
				results[i], _ = instances[i].UpdateSkill(ctx, api.UpdateSkillRequestObject{
					Name: "refund-policy", Params: api.UpdateSkillParams{IfMatch: &h},
					Body: &api.UpdateSkillJSONRequestBody{Content: contents[i]},
				})
			})
		}
		wg.Wait()
		wins, conflicts := 0, 0
		for _, r := range results {
			switch v := r.(type) {
			case api.UpdateSkill200JSONResponse:
				wins++
				hash = v.Hash
			case api.UpdateSkill409JSONResponse:
				conflicts++
				require.NotNil(t, v.Hash)
			}
		}
		require.Equal(t, 1, wins, "round %d", round)
		require.Equal(t, 1, conflicts, "round %d", round)
	}
}

// A viewer reads skills but cannot change them.
func TestSkillsWriteNeedsDeveloper(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	apiImpl := localapi.New(
		helper.DAGRepository, helper.DAGRunRepository, helper.QueueStore, helper.ProcRepository,
		helper.DAGRunMgr, helper.Config, nil, helper.ServiceRegistry, nil, nil,
	)
	viewer := auth.WithUser(context.Background(), &auth.User{
		ID: "cloud:v", Role: auth.RoleViewer, AuthProvider: auth.AuthProviderCloud,
	})
	h := strings.Repeat("0", 64)

	_, err := apiImpl.ListSkills(viewer, api.ListSkillsRequestObject{})
	require.NoError(t, err)
	_, err = apiImpl.DownloadSkills(viewer, api.DownloadSkillsRequestObject{Body: &api.DownloadSkillsJSONRequestBody{Names: []string{"x"}}})
	require.Error(t, err, "the download needs a skill that exists, and no role")
	_, err = apiImpl.UpdateSkill(viewer, api.UpdateSkillRequestObject{
		Name: "refund-policy", Body: &api.UpdateSkillJSONRequestBody{Content: validSkill},
	})
	require.Error(t, err)
	_, err = apiImpl.DeleteSkill(viewer, api.DeleteSkillRequestObject{Name: "refund-policy", Params: api.DeleteSkillParams{IfMatch: &h}})
	require.Error(t, err)
	_, err = apiImpl.PutSkillFile(viewer, api.PutSkillFileRequestObject{Name: "refund-policy", Params: api.PutSkillFileParams{Path: "references/a.md", IfMatch: &h}, Body: strings.NewReader("x")})
	require.Error(t, err)
	_, err = apiImpl.DeleteSkillFile(viewer, api.DeleteSkillFileRequestObject{Name: "refund-policy", Params: api.DeleteSkillFileParams{Path: "references/a.md", IfMatch: &h}})
	require.Error(t, err)
}
