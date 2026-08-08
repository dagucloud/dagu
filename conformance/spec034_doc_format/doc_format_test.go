// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec034_doc_format_test

import (
	"net/http"
	"net/url"
	"testing"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentFileFormat(t *testing.T) {
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
		server.Client().Post("/api/v1/docs", api.CreateDocRequest{
			Id:      "guides/ETL Runbook_v1.2",
			Content: content,
		}).ExpectStatus(http.StatusCreated).Send(t)

		resp := server.Client().Get("/api/v1/docs/doc?path=" + url.QueryEscape("guides/ETL Runbook_v1.2")).
			ExpectStatus(http.StatusOK).Send(t)
		var doc api.DocResponse
		resp.Unmarshal(t, &doc)
		assert.Equal(t, "ETL Runbook", doc.Title)
		assert.Equal(t, "Recovery steps.", doc.Description)
		require.NotNil(t, doc.Tags)
		assert.Equal(t, []string{"ops", "Runbook"}, *doc.Tags)
		assert.Equal(t, content, doc.Content)

		for _, id := range []string{"bad:name", "CON", "trailing."} {
			server.Client().Post("/api/v1/docs", api.CreateDocRequest{Id: id, Content: "body"}).
				ExpectStatus(http.StatusBadRequest).Send(t)
		}
	})

	t.Run("wiki link exclusions", func(t *testing.T) {
		content := "[[guides/target]]\n\n`code starts\n[[hidden-inline]]\ncode ends`\n\n```\n[[hidden-fence]]\n```\n\n![[logo.png]]\n[[dag:daily-etl]]\n"
		server.Client().Post("/api/v1/docs", api.CreateDocRequest{
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
	resp := server.Client().Get("/api/v1/docs/backlinks?target=" + url.QueryEscape(target)).
		ExpectStatus(http.StatusOK).Send(t)
	var backlinks api.DocBacklinksResponse
	resp.Unmarshal(t, &backlinks)
	ids := make([]string, len(backlinks.Items))
	for i, item := range backlinks.Items {
		ids[i] = item.Id
	}
	return ids
}
