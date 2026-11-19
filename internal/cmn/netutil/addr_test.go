// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package netutil_test

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/netutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAddrInUse(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	_, err = net.Listen("tcp4", listener.Addr().String())
	require.Error(t, err)

	assert.True(t, netutil.IsAddrInUse(err))
	assert.True(t, netutil.IsAddrInUse(errors.Join(errors.New("cleanup"), fmt.Errorf("start: %w", err))))
	assert.False(t, netutil.IsAddrInUse(errors.New("connection refused")))
	assert.False(t, netutil.IsAddrInUse(nil))
}
