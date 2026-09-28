// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	// providerName identifies computer steps in agent sessions.
	providerName = computerhost.AgentProvider
	// artifactsSubdir holds the screenshots of computer steps in the run
	// artifacts directory.
	artifactsSubdir = "computer"

	// Reasons a computer step waits, which name its waiting timeline events:
	// another step holds the desktop, or a person is using it.
	waitReasonDesktop = "desktop"
	waitReasonPerson  = "person"
)

// logAction logs one desktop action of the operation at index.
func logAction(t *agentstep.Timeline, index int, description string) {
	_, _ = fmt.Fprintf(t.Log, "%s   %s\n", t.Position(index), t.Masker.MaskString(description))
}
