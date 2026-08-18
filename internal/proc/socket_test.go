// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

func TestDAGSocketAddr(t *testing.T) {
	t.Parallel()

	ref := ir.NewDAGRunRef("mydag", "run123")
	require.Equal(t, sock.Addr("mydag", "run123"), DAGSocketAddr(ref))
}
