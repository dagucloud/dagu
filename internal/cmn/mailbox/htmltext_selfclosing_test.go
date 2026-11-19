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

	got := htmlToText(strings.NewReader("<head><style/></head><body>Hello</body>"))
	assert.Contains(t, got, "Hello")
	assert.NotContains(t, got, "<body>")
	assert.NotContains(t, got, "</head>")
}
