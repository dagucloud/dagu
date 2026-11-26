// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
)

// allCapabilities has the six capabilities of the check 7.
const allCapabilities = uint64(1<<0 | 1<<1 | 1<<3 | 1<<5 | 1<<6 | 1<<7)

// Spec 092, "Start", checks 6 to 8, in this order, with the error of the first
// check that fails.
func TestAgentWorkerCheck(t *testing.T) {
	t.Parallel()

	enabled := config.UntrustedConfig{Enabled: true, UID: 10001, GID: 10001}
	ok := privileges{euid: 0, capEff: allCapabilities, noNewPrivs: true}
	for _, tc := range []struct {
		name string
		cfg  config.UntrustedConfig
		priv privileges
		err  error
		want string
	}{
		{"no restricted user", config.UntrustedConfig{}, ok, nil, `--agent: key "untrusted.uid" is not set`},
		{"isolation off", config.UntrustedConfig{IsolationOff: true}, ok, nil, `--agent: key "untrusted.isolation" is "off", not "on"`},
		{"not root", enabled, privileges{euid: 1000, capEff: allCapabilities, noNewPrivs: true}, nil, "--agent: the effective user ID is 1000, not 0"},
		{"no CAP_SETUID", enabled, privileges{capEff: allCapabilities &^ (1 << 7), noNewPrivs: true}, nil, "--agent: the effective capability CAP_SETUID is absent"},
		{"no CAP_KILL", enabled, privileges{capEff: allCapabilities &^ (1 << 5), noNewPrivs: true}, nil, "--agent: the effective capability CAP_KILL is absent"},
		{"no no-new-privileges", enabled, privileges{capEff: allCapabilities}, nil, "--agent: NoNewPrivs is 0 in /proc/self/status, not 1"},
		{"status unreadable", enabled, privileges{}, errors.New("read /proc/self/status: denied"), "--agent: read /proc/self/status: denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := agentWorkerCheck(tc.cfg, tc.priv, tc.err)
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), tc.want)
			}
		})
	}
	assert.NoError(t, agentWorkerCheck(enabled, ok, nil))
}
