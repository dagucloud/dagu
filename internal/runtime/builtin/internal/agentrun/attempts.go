// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentrun

import (
	"fmt"
	"io"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// ReportFailedAttempt writes the line that Spec 091 requires for a failed
// attempt: "agent: attempt ended <kind>".
func ReportFailedAttempt(w io.Writer, kind ir.AgentFailureKind) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "agent: attempt ended %s\n", kind)
}
