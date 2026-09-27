// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

// providerName identifies browser steps in agent sessions.
const providerName = browserhost.AgentProvider

// statusBlocked marks requests allowed_domains blocked.
const statusBlocked = "blocked"

// logBlocked reports the requests allowed_domains blocked while the
// operation at index ran, summarized by describeBlocked.
func logBlocked(t *agentstep.Timeline, index int, summary string) {
	summary = t.Masker.MaskString(summary)
	_, _ = fmt.Fprintf(t.Log, "%s %s %s %s\n", t.Position(index), kindAllowedDomains, statusBlocked, summary)
	t.AppendEvent(ir.AgentSessionEvent{Type: agentstep.EventOperation, Name: kindAllowedDomains, Status: statusBlocked, Content: summary})
}
