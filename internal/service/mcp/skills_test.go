// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	bundledskills "github.com/dagucloud/dagu/v2/skills"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const daguSkillURI = "skill://dagu/SKILL.md"

// The listed entry must be a complete manifest: every bundled file, each
// readable with the advertised digest and size, and frontmatter equal to the
// one SKILL.md carries.
func TestSkillsList(t *testing.T) {
	ctx := context.Background()
	session := connectSkillsClient(t, ctx)

	list, err := mcpsdk.CallCustomMethod[*listSkillsParams, *listSkillsResult](ctx, session, methodListSkills, &listSkillsParams{})
	require.NoError(t, err)
	requireCompleteCacheable(t, list.ResultType, list.Cacheable)
	require.Len(t, list.Skills, 1)
	entry := list.Skills[0]
	require.Equal(t, daguSkillURI, entry.URI)

	var listed []string
	for _, res := range entry.Resources {
		listed = append(listed, res.URI)
		read, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: res.URI})
		require.NoError(t, err)
		require.Len(t, read.Contents, 1)
		require.Equal(t, resourceMIMEText, read.Contents[0].MIMEType)
		require.Equal(t, list.TTLMs, read.TTLMs)
		require.Equal(t, list.CacheScope, read.CacheScope)
		data := []byte(read.Contents[0].Text)
		sum := sha256.Sum256(data)
		require.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), res.Digest, res.URI)
		require.Equal(t, len(data), res.Size, res.URI)
	}
	require.ElementsMatch(t, bundledSkillURIs(t), listed)

	var frontmatter map[string]any
	require.NoError(t, json.Unmarshal(entry.Frontmatter, &frontmatter))
	resources, err := session.ListResources(ctx, nil)
	require.NoError(t, err)
	skillMD := findResource(t, resources.Resources, daguSkillURI)
	require.Equal(t, "dagu", frontmatter["name"])
	require.Equal(t, "dagu", skillMD.Name)
	require.NotEmpty(t, skillMD.Description)
	require.Equal(t, frontmatter["description"], skillMD.Description)

	// The listing is one page, so any cursor is one the server never issued.
	_, err = mcpsdk.CallCustomMethod[*listSkillsParams, *listSkillsResult](ctx, session, methodListSkills, &listSkillsParams{Cursor: "next"})
	requireInvalidParams(t, err)
}

func TestSkillsGet(t *testing.T) {
	ctx := context.Background()
	session := connectSkillsClient(t, ctx)

	list, err := mcpsdk.CallCustomMethod[*listSkillsParams, *listSkillsResult](ctx, session, methodListSkills, &listSkillsParams{})
	require.NoError(t, err)

	got, err := mcpsdk.CallCustomMethod[*getSkillParams, *getSkillResult](ctx, session, methodGetSkill, &getSkillParams{URI: daguSkillURI})
	require.NoError(t, err)
	requireCompleteCacheable(t, got.ResultType, got.Cacheable)
	require.Equal(t, list.Skills[0], got.Skill)

	_, err = mcpsdk.CallCustomMethod[*getSkillParams, *getSkillResult](ctx, session, methodGetSkill, &getSkillParams{URI: "skill://other/SKILL.md"})
	requireInvalidParams(t, err)
}

// A Windows checkout embeds the skill with CRLF line endings. The catalog must
// parse it and still describe the bytes it serves.
func TestSkillCatalog(t *testing.T) {
	const skillMD = "---\nname: demo\ndescription: Does demo work\nlicense: MIT\nmetadata:\n  team: core\n---\n\n# Demo\n"
	crlf := strings.ReplaceAll(skillMD, "\n", "\r\n")
	want := map[string]any{
		"name":        "demo",
		"description": "Does demo work",
		"license":     "MIT",
		"metadata":    map[string]any{"team": "core"},
	}

	tests := []struct {
		name    string
		files   fstest.MapFS
		wantErr string
	}{
		{name: "LF", files: fstest.MapFS{"demo/SKILL.md": {Data: []byte(skillMD)}, "demo/references/a.md": {Data: []byte("a\n")}}},
		{name: "CRLF", files: fstest.MapFS{"demo/SKILL.md": {Data: []byte(crlf)}, "demo/references/a.md": {Data: []byte("a\r\n")}}},
		{name: "NameMismatch", files: fstest.MapFS{"demo/SKILL.md": {Data: []byte(strings.Replace(skillMD, "name: demo", "name: other", 1))}}, wantErr: "differs"},
		{name: "NoSkillMD", files: fstest.MapFS{"demo/references/a.md": {Data: []byte("a\n")}}, wantErr: "SKILL.md is missing"},
		{name: "NoFrontmatter", files: fstest.MapFS{"demo/SKILL.md": {Data: []byte("# Demo\n")}}, wantErr: "frontmatter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog, err := newSkillCatalog(tt.files, "demo")
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, catalog.skills, 1)
			entry := catalog.skills[0]
			require.Equal(t, "skill://demo/SKILL.md", entry.URI)
			var got map[string]any
			require.NoError(t, json.Unmarshal(entry.Frontmatter, &got))
			require.Equal(t, want, got)

			require.Len(t, catalog.files, len(tt.files))
			for i, file := range catalog.files {
				data := tt.files["demo/"+strings.TrimPrefix(file.uri, "skill://demo/")].Data
				require.Equal(t, string(data), file.text)
				sum := sha256.Sum256(data)
				require.Equal(t, skillResource{URI: file.uri, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: len(data)}, entry.Resources[i])
			}
			require.Equal(t, "demo", catalog.files[0].name)
			require.Equal(t, "Does demo work", catalog.files[0].description)
		})
	}
}

// connectSkillsClient connects a client that can send the skills methods over
// the protocol revision the extension is specified against.
func connectSkillsClient(t *testing.T, ctx context.Context) *mcpsdk.ClientSession {
	t.Helper()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "dagu-mcp-test", Version: "v0.0.0"}, nil)
	require.NoError(t, mcpsdk.AddSendingCustomMethod[*listSkillsParams, *listSkillsResult](client, methodListSkills))
	require.NoError(t, mcpsdk.AddSendingCustomMethod[*getSkillParams, *getSkillResult](client, methodGetSkill))
	session := connectClient(t, ctx, NewServer(nil), client)
	require.Equal(t, "2026-07-28", session.InitializeResult().ProtocolVersion)
	return session
}

func requireCompleteCacheable(t *testing.T, resultType string, cache mcpsdk.Cacheable) {
	t.Helper()
	require.Equal(t, "complete", resultType)
	require.Positive(t, cache.TTLMs)
	require.Equal(t, "public", cache.CacheScope)
}

func requireInvalidParams(t *testing.T, err error) {
	t.Helper()
	var rpcErr *jsonrpc.Error
	require.True(t, errors.As(err, &rpcErr), "got %v", err)
	require.Equal(t, int64(jsonrpc.CodeInvalidParams), rpcErr.Code)
}

// bundledSkillURIs returns the URI of every file embedded for the dagu skill.
func bundledSkillURIs(t *testing.T) []string {
	t.Helper()
	var uris []string
	dir := bundledskills.DaguSkillDir
	require.NoError(t, fs.WalkDir(bundledskills.Assets, dir, func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			uris = append(uris, "skill://dagu/"+strings.TrimPrefix(name, dir+"/"))
		}
		return err
	}))
	require.NotEmpty(t, uris)
	return uris
}
