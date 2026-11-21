// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A self-closing raw-text tag must not leave the tokenizer in raw mode nor
// the skip counter stuck: either would drop the following body or leak its
// markup into the plain-text preview used by mail search.
func TestHTMLToTextSelfClosingRawTextTag(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		html string
		want string
	}{
		{"in head", "<head><style/></head><body>Hello</body>", "Hello"},
		{"outside head", "<style/>Hello<body>World</body>", "World"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := htmlToText(strings.NewReader(tc.html))
			assert.Contains(t, got, tc.want)
			assert.NotContains(t, got, "<body>")
			assert.NotContains(t, got, "</head>")
		})
	}
}

// An end tag that never had a matching start tag must not close the skip
// opened by an ancestor (e.g. <head>), which would expose its hidden text.
func TestHTMLToTextUnmatchedEndTagDoesNotCloseOuterSkip(t *testing.T) {
	t.Parallel()

	got := htmlToText(strings.NewReader("<head><title/></title>hidden</head><body>visible</body>"))
	assert.NotContains(t, got, "hidden")
	assert.Contains(t, got, "visible")
}
