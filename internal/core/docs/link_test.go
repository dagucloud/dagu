// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractWikiLinks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		links   []WikiLink
	}{
		{
			name:    "plain link",
			content: "see [[guides/deploy]] for details",
			links:   []WikiLink{{Target: "guides/deploy"}},
		},
		{
			name:    "labeled link",
			content: "see [[guides/deploy|the deploy guide]]",
			links:   []WikiLink{{Target: "guides/deploy", Label: "the deploy guide"}},
		},
		{
			name:    "anchored link",
			content: "see [[guides/deploy#rollback]]",
			links:   []WikiLink{{Target: "guides/deploy", Anchor: "rollback"}},
		},
		{
			name:    "anchored labeled link",
			content: "see [[guides/deploy#rollback|rollback steps]]",
			links:   []WikiLink{{Target: "guides/deploy", Anchor: "rollback", Label: "rollback steps"}},
		},
		{
			name:    "scheme target kept raw",
			content: "status: [[dag:daily-etl|ETL]]",
			links:   []WikiLink{{Target: "dag:daily-etl", Label: "ETL"}},
		},
		{
			name:    "multiple links in document order",
			content: "[[a]] then [[b]]\nthen [[c]]",
			links:   []WikiLink{{Target: "a"}, {Target: "b"}, {Target: "c"}},
		},
		{
			name:    "fenced code excluded",
			content: "before\n```\n[[ignored]]\n```\n[[kept]]",
			links:   []WikiLink{{Target: "kept"}},
		},
		{
			name:    "tilde fence excluded",
			content: "~~~\n[[ignored]]\n~~~\n[[kept]]",
			links:   []WikiLink{{Target: "kept"}},
		},
		{
			name:    "inline code excluded",
			content: "use `[[ignored]]` but [[kept]]",
			links:   []WikiLink{{Target: "kept"}},
		},
		{
			name:    "malformed links ignored",
			content: "[[]] [[ ]] [[a|b|c]]",
			links:   []WikiLink{{Target: "a", Label: "b|c"}},
		},
		{
			name:    "no links",
			content: "plain [markdown](link) only",
			links:   nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.links, ExtractWikiLinks(tc.content))
		})
	}
}
