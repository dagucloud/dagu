// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec034_wiki_page_format_test

import (
	"net/http"
	"net/url"
	"testing"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiPageFileFormat(t *testing.T) {
	server := test.SetupServer(t)

	t.Run("identity and frontmatter round trip", func(t *testing.T) {
		content := `---
title: ETL Runbook
description: Recovery steps.
tags: [ops, Runbook, OPS]
custom: preserved
---

# ETL Runbook
`
		server.Client().Post("/api/v1/wiki", api.CreateWikiPageRequest{
			Id:      "guides/ETL Runbook_v1.2",
			Content: content,
		}).ExpectStatus(http.StatusCreated).Send(t)

		resp := server.Client().Get("/api/v1/wiki/page?path=" + url.QueryEscape("guides/ETL Runbook_v1.2")).
			ExpectStatus(http.StatusOK).Send(t)
		var page api.WikiPageResponse
		resp.Unmarshal(t, &page)
		assert.Equal(t, "ETL Runbook", page.Title)
		assert.Equal(t, "Recovery steps.", page.Description)
		require.NotNil(t, page.Tags)
		assert.Equal(t, []string{"ops", "Runbook"}, *page.Tags)
		assert.Equal(t, content, page.Content)

		for _, id := range []string{"bad:name", "CON", "trailing."} {
			server.Client().Post("/api/v1/wiki", api.CreateWikiPageRequest{Id: id, Content: "body"}).
				ExpectStatus(http.StatusBadRequest).Send(t)
		}
	})

	t.Run("wiki link exclusions", func(t *testing.T) {
		content := "[[guides/target]]\n\n`code starts\n[[hidden-inline]]\ncode ends`\n\n```\n[[hidden-fence]]\n```\n\n![[logo.png]]\n[[dag:daily-etl]]\n"
		server.Client().Post("/api/v1/wiki", api.CreateWikiPageRequest{
			Id:      "source",
			Content: content,
		}).ExpectStatus(http.StatusCreated).Send(t)

		assert.Equal(t, []string{"source"}, backlinkIDs(t, server, "guides/target"))
		assert.Equal(t, []string{"source"}, backlinkIDs(t, server, "dag:daily-etl"))
		assert.Empty(t, backlinkIDs(t, server, "hidden-inline"))
		assert.Empty(t, backlinkIDs(t, server, "hidden-fence"))
		assert.Empty(t, backlinkIDs(t, server, "logo.png"))
	})
}

func backlinkIDs(t *testing.T, server test.Server, target string) []string {
	t.Helper()
	resp := server.Client().Get("/api/v1/wiki/backlinks?target=" + url.QueryEscape(target)).
		ExpectStatus(http.StatusOK).Send(t)
	var backlinks api.WikiPageBacklinksResponse
	resp.Unmarshal(t, &backlinks)
	ids := make([]string, len(backlinks.Items))
	for i, item := range backlinks.Items {
		ids[i] = item.Id
	}
	return ids
}
