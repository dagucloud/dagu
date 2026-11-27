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

	bundledskills "github.com/dagucloud/dagu/v2/skills"
	"github.com/goccy/go-yaml"
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
		data := []byte(read.Contents[0].Text)
		sum := sha256.Sum256(data)
		require.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), res.Digest, res.URI)
		require.Equal(t, len(data), res.Size, res.URI)

		if res.URI == daguSkillURI {
			var got map[string]any
			require.NoError(t, json.Unmarshal(entry.Frontmatter, &got))
			require.Equal(t, skillFrontmatter(t, read.Contents[0].Text), got)
			require.Equal(t, "dagu", got["name"])
		}
	}
	require.ElementsMatch(t, bundledSkillURIs(t), listed)

	resources, err := session.ListResources(ctx, nil)
	require.NoError(t, err)
	skillMD := findResource(t, resources.Resources, daguSkillURI)
	require.Equal(t, "dagu", skillMD.Name)
	require.NotEmpty(t, skillMD.Description)
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
	var rpcErr *jsonrpc.Error
	require.True(t, errors.As(err, &rpcErr), "got %v", err)
	require.Equal(t, int64(jsonrpc.CodeInvalidParams), rpcErr.Code)
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

// skillFrontmatter parses the YAML frontmatter at the start of a SKILL.md.
func skillFrontmatter(t *testing.T, text string) map[string]any {
	t.Helper()
	rest, ok := strings.CutPrefix(text, "---\n")
	require.True(t, ok)
	front, _, ok := strings.Cut(rest, "\n---\n")
	require.True(t, ok)
	var fields map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(front), &fields))
	return fields
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
