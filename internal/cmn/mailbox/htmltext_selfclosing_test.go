// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A self-closing script/style/head/title tag must not leave the text skip
// counter stuck: no end tag will ever arrive to balance it, so all following
// mail body text would be silently dropped.
func TestHTMLToTextSelfClosingStyleDoesNotSwallowBody(t *testing.T) {
	t.Parallel()

	got := htmlToText(strings.NewReader("<style/>hello<body>world</body>"))
	assert.Contains(t, got, "hello")
	assert.Contains(t, got, "world")
}
