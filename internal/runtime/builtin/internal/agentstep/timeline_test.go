// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep_test

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimelineOperation(t *testing.T) {
	t.Parallel()

	var log bytes.Buffer
	session := &ir.AgentSession{Generation: 2}
	timeline := &agentstep.Timeline{
		Log:      &log,
		Masker:   agentstep.NewMasker(map[string]string{"TOKEN": "tok-12345"}, nil),
		Total:    3,
		Update:   func(fn func(*ir.AgentSession)) { fn(session) },
		Provider: "computer",
	}
	timeline.Operation(agentstep.Report{
		Index: 1, Kind: "act", Subject: "Type tok-12345", Status: agentstep.StatusCompleted, Via: agentstep.ViaModel,
		Detail: "Done", Tokens: 12, Duration: 1500 * time.Millisecond, Files: []string{"computer/post/01-act.png"},
	})

	assert.Equal(t, "[2/3] act \"Type *******\" → Done (completed, 12 tokens, 1.5s, via model)\n", log.String())
	require.Len(t, session.Events, 1)
	event := session.Events[0]
	assert.Equal(t, "computer-2-1", event.ID)
	assert.Equal(t, agentstep.EventOperation, event.Type)
	assert.Equal(t, "Type *******\nDone", event.Content)
	assert.Equal(t, []string{"computer/post/01-act.png"}, event.Files)
	assert.Equal(t, agentstep.ViaModel, event.Via)
	assert.Equal(t, int64(1500), event.DurationMs)
	assert.Equal(t, int64(12), event.Tokens)
}

// Numbering continues across step executions, so a retry keeps the
// screenshots of earlier attempts.
func TestArtifactStoreScreenshots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, err := agentstep.NewArtifactStore(root, "computer", "post").WriteScreenshot("act", []byte("png"))
	require.NoError(t, err)
	assert.Equal(t, "computer/post/01-act.png", first)

	second, err := agentstep.NewArtifactStore(root, "computer", "post").WriteScreenshot("failure", []byte("png"))
	require.NoError(t, err)
	assert.Equal(t, "computer/post/02-failure.png", second)

	_, err = agentstep.NewArtifactStore("", "computer", "post").WriteScreenshot("act", nil)
	require.ErrorIs(t, err, agentstep.ErrNoArtifactStorage)
}

// Long event content is cut to the event size limit on a character
// boundary, so the stored text stays valid UTF-8.
func TestTimelineTruncatesOnCharacterBoundary(t *testing.T) {
	t.Parallel()

	session := &ir.AgentSession{}
	timeline := &agentstep.Timeline{Log: io.Discard, Masker: agentstep.NewMasker(nil, nil), Update: func(fn func(*ir.AgentSession)) { fn(session) }}
	// 4095 ASCII bytes followed by a three-byte character straddle the
	// 4096-byte limit.
	content := strings.Repeat("a", 4095) + "日本"
	timeline.AppendEvent(ir.AgentSessionEvent{Content: content})

	stored := session.Events[0].Content
	assert.True(t, utf8.ValidString(stored))
	assert.Equal(t, strings.Repeat("a", 4095), stored)
}
