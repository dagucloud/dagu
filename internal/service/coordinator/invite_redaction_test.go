// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
)

func TestRedactInvites(t *testing.T) {
	t.Parallel()

	invite := "dagu1." + strings.Repeat("A", 43) + "." + strings.Repeat("b-_", 14) + "c"
	for in, want := range map[string]string{
		"/api/v1/workers/invites/" + invite:          "/api/v1/workers/invites/[REDACTED]",
		"expiresIn=" + invite + "&address=" + invite: "expiresIn=[REDACTED]&address=[REDACTED]",
		"/api/v1/workers/invites/invite-1":           "/api/v1/workers/invites/invite-1",
		"dagu1.short.secret":                         "dagu1.short.secret",
	} {
		assert.Equal(t, want, coordinator.RedactInvites(in), in)
	}
}
