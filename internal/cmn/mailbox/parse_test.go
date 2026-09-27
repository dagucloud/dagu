// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A header block the MIME parser rejects still yields only the body as text.
func TestParseBodyKeepsHeadersOutOfUnparsableMail(t *testing.T) {
	t.Parallel()

	raw := "Subject: Status\r\nThis line is not a header\r\n\r\nBody text\r\n"
	msg := Message{}
	require.NoError(t, parseBody([]byte(raw), &msg, &attachmentSaver{}))
	assert.Equal(t, "Body text", msg.Text)
}
