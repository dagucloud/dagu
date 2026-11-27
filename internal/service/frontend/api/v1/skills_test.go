// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
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

func getSkill(t *testing.T, server test.Server, name string) api.SkillDetail {
	t.Helper()
	var out api.GetSkill200JSONResponse
	server.Client().Get("/api/v1/skills/"+name).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &out)
	return api.SkillDetail(out)
}

func skillIfMatch(hash string) map[string]string { return map[string]string{"If-Match": hash} }

func putSkill(server test.Server, path string, body any, headers map[string]string) *test.Request {
	r := server.Client().Put(path, body)
	for k, v := range headers {
		r = r.WithHeader(k, v)
	}
	return r
}

func deleteSkill(server test.Server, path string, headers map[string]string) *test.Request {
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

	putSkill(server, "/api/v1/skills/refund-policy", body, nil).ExpectStatus(http.StatusPreconditionRequired).Send(t)
	for _, value := range []string{"nope", strings.Repeat("z", 64)} {
		putSkill(server, "/api/v1/skills/refund-policy", body, skillIfMatch(value)).ExpectStatus(http.StatusBadRequest).Send(t)
	}
	_, err := os.Stat(filepath.Join(skillsDir(server), "refund-policy"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestSkillsPutChainAndConflict(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	create := map[string]string{"If-None-Match": "*"}
	file := filepath.Join(skillsDir(server), "refund-policy", "SKILL.md")

	var created api.UpdateSkill200JSONResponse
	putSkill(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: validSkill}, create).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &created)
	assert.Len(t, created.Hash, 64)
	assert.Empty(t, created.Errors)
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, validSkill, string(data))
	assert.Equal(t, created.Hash, getSkill(t, server, "refund-policy").Hash)

	t.Run("CreateOfAnExistingSkillConflicts", func(t *testing.T) {
		var out api.SkillError
		putSkill(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: validSkill}, create).
			ExpectStatus(http.StatusConflict).Send(t).Unmarshal(t, &out)
		assert.Equal(t, api.ErrorCodeConflict, out.Code)
		require.NotNil(t, out.Hash)
		assert.Equal(t, created.Hash, *out.Hash)
	})

	next := "---\nname: refund-policy\ndescription: Changed rules to refund an order.\n---\nNew body\n"
	var updated api.UpdateSkill200JSONResponse
	t.Run("OverwriteWithTheHash", func(t *testing.T) {
		putSkill(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: next}, skillIfMatch(created.Hash)).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &updated)
		assert.NotEqual(t, created.Hash, updated.Hash)
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Equal(t, next, string(data))
	})

	t.Run("StaleHashConflictsAndKeepsTheFile", func(t *testing.T) {
		var out api.SkillError
		putSkill(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: validSkill}, skillIfMatch(created.Hash)).
			ExpectStatus(http.StatusConflict).Send(t).Unmarshal(t, &out)
		require.NotNil(t, out.Hash)
		assert.Equal(t, updated.Hash, *out.Hash, "the conflict gives the current hash")
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Equal(t, next, string(data))
	})

	t.Run("AChangeOnDiskIsAConflict", func(t *testing.T) {
		require.NoError(t, os.WriteFile(file, []byte(strings.Replace(next, "New body", "Edited by hand", 1)), 0o600))
		putSkill(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: next}, skillIfMatch(updated.Hash)).
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
			putSkill(server, "/api/v1/skills/refund-policy", api.UpdateSkillJSONRequestBody{Content: content}, skillIfMatch(cur)).
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
			putSkill(server, "/api/v1/skills/"+name, body, create).ExpectStatus(http.StatusBadRequest).Send(t)
		}
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

	deleteSkill(server, "/api/v1/skills/refund-policy", nil).ExpectStatus(http.StatusPreconditionRequired).Send(t)
	deleteSkill(server, "/api/v1/skills/refund-policy", skillIfMatch(strings.Repeat("0", 64))).ExpectStatus(http.StatusConflict).Send(t)
	deleteSkill(server, "/api/v1/skills/refund-policy", map[string]string{"If-Match": h, "If-None-Match": "*"}).ExpectStatus(http.StatusBadRequest).Send(t)
	deleteSkill(server, "/api/v1/skills/refund-policy", skillIfMatch(h)).ExpectStatus(http.StatusNoContent).Send(t)
	_, err := os.Stat(filepath.Join(skillsDir(server), "refund-policy"))
	assert.ErrorIs(t, err, os.ErrNotExist, "the whole folder goes")
	deleteSkill(server, "/api/v1/skills/refund-policy", skillIfMatch(h)).ExpectStatus(http.StatusNotFound).Send(t)
	deleteSkill(server, "/api/v1/skills/.x", skillIfMatch(h)).ExpectStatus(http.StatusBadRequest).Send(t)
}

// A file is sent as stored, as an attachment of type application/octet-stream,
// with the hash of the skill in ETag. A link in the folder is never followed.
func TestSkillsReadFile(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t)
	writeSkillFile(t, server, "refund-policy", validSkill)
	folder := filepath.Join(skillsDir(server), "refund-policy")
	write := func(rel string, data []byte) {
		t.Helper()
		full := filepath.Join(folder, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, data, 0o600))
	}
	write("references/faq.md", []byte("# FAQ\n"))
	write("assets/logo.bin", []byte{0, 1, 2, 255})
	write("assets/page.html", []byte("<script>alert(1)</script>"))
	secret := filepath.Join(t.TempDir(), "secret.md")
	require.NoError(t, os.WriteFile(secret, []byte("secret"), 0o600))
	require.NoError(t, os.Symlink(secret, filepath.Join(folder, "references", "link.md")))
	hash := getSkill(t, server, "refund-policy").Hash
	get := func(rel string, status int) *test.Response {
		return server.Client().Get("/api/v1/skills/refund-policy/files?path=" + rel).ExpectStatus(status).Send(t)
	}

	for rel, want := range map[string][]byte{
		"references/faq.md": []byte("# FAQ\n"),
		"assets/logo.bin":   {0, 1, 2, 255},
		"assets/page.html":  []byte("<script>alert(1)</script>"),
	} {
		resp := get(rel, http.StatusOK)
		assert.Equal(t, want, resp.Response.Body(), rel)
		assert.Equal(t, `"`+hash+`"`, resp.Response.Header().Get("ETag"), rel)
		assert.Equal(t, "application/octet-stream", resp.Response.Header().Get("Content-Type"), rel)
		assert.True(t, strings.HasPrefix(resp.Response.Header().Get("Content-Disposition"), "attachment;"), rel)
	}

	write("assets/big.bin", bytes.Repeat([]byte("x"), skill.MaxFileBytes+1))
	get("assets/big.bin", http.StatusRequestEntityTooLarge)
	get("references/none.md", http.StatusNotFound)
	assert.NotContains(t, get("references/link.md", http.StatusNotFound).Body, "secret")
	get("..%2F..%2FSKILL.md", http.StatusBadRequest)
	get("%2Fetc%2Fpasswd", http.StatusBadRequest)
	server.Client().Get("/api/v1/skills/missing/files?path=SKILL.md").ExpectStatus(http.StatusNotFound).Send(t)
}

// A file of a skill that the server cannot read gives 500, not 404, and the
// body names no server path. The test needs a user other than root.
func TestSkillsReadFileUnreadable(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a folder of mode 0000")
	}
	if runtime.GOOS == "windows" {
		t.Skip("a folder mode of 0 does not stop a read on Windows")
	}
	server := test.SetupServer(t)
	writeSkillFile(t, server, "refund-policy", validSkill)
	locked := filepath.Join(skillsDir(server), "refund-policy", "references")
	require.NoError(t, os.MkdirAll(locked, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(locked, "faq.md"), []byte("# FAQ\n"), 0o600))
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	resp := server.Client().Get("/api/v1/skills/refund-policy/files?path=references/faq.md").
		ExpectStatus(http.StatusInternalServerError).Send(t)
	assert.NotContains(t, resp.Body, skillsDir(server))
}

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
				req = putSkill(server, "/api/v1/skills/"+name, api.UpdateSkillJSONRequestBody{Content: validSkill}, map[string]string{"If-None-Match": "*"})
			default:
				req = deleteSkill(server, "/api/v1/skills/"+name, skillIfMatch(strings.Repeat("0", 64)))
			}
			status := req.Send(t).Response.StatusCode()
			assert.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, status, "%s %s", method, name)
		}
	}

	_, err := os.Stat(outside)
	require.NoError(t, err, "file outside the skills directory must stay")
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

// A viewer reads skills but cannot change them. Each change is a request that
// a developer could make, with the current hash, so only the role refuses it.
func TestSkillsWriteNeedsDeveloper(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	apiImpl := localapi.New(
		helper.DAGRepository, helper.DAGRunRepository, helper.QueueStore, helper.ProcRepository,
		helper.DAGRunMgr, helper.Config, nil, helper.ServiceRegistry, nil, nil,
	)
	asRole := func(role auth.Role) context.Context {
		return auth.WithUser(context.Background(), &auth.User{
			ID: "cloud:" + string(role), Role: role, AuthProvider: auth.AuthProviderCloud,
		})
	}
	viewer, developer := asRole(auth.RoleViewer), asRole(auth.RoleDeveloper)
	none := "*"
	created, err := apiImpl.UpdateSkill(developer, api.UpdateSkillRequestObject{
		Name: "refund-policy", Params: api.UpdateSkillParams{IfNoneMatch: &none},
		Body: &api.UpdateSkillJSONRequestBody{Content: validSkill},
	})
	require.NoError(t, err)
	h := created.(api.UpdateSkill200JSONResponse).Hash

	_, err = apiImpl.ListSkills(viewer, api.ListSkillsRequestObject{})
	require.NoError(t, err)

	_, err = apiImpl.UpdateSkill(viewer, api.UpdateSkillRequestObject{
		Name: "refund-policy", Params: api.UpdateSkillParams{IfMatch: &h},
		Body: &api.UpdateSkillJSONRequestBody{Content: validSkill + "More.\n"},
	})
	requireAPIErrorStatus(t, err, http.StatusForbidden)
	_, err = apiImpl.DeleteSkill(viewer, api.DeleteSkillRequestObject{Name: "refund-policy", Params: api.DeleteSkillParams{IfMatch: &h}})
	requireAPIErrorStatus(t, err, http.StatusForbidden)

	got, err := apiImpl.GetSkill(viewer, api.GetSkillRequestObject{Name: "refund-policy"})
	require.NoError(t, err)
	require.Equal(t, h, got.(api.GetSkill200JSONResponse).Hash, "the refused changes left the skill as it was")
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
