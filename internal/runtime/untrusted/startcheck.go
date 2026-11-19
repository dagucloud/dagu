// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// capability is one Linux capability that the change to the restricted user
// needs.
type capability struct {
	name string
	bit  uint
}

// requiredCapabilities lists the effective capabilities of Spec 092, "Start",
// check 7, in the order of the spec.
var requiredCapabilities = []capability{
	{"CAP_SETUID", 7},
	{"CAP_SETGID", 6},
	{"CAP_CHOWN", 0},
	{"CAP_FOWNER", 3},
	{"CAP_DAC_OVERRIDE", 1},
	{"CAP_KILL", 5},
}

// privileges is what /proc/self/status tells about the process.
type privileges struct {
	euid       int
	capEff     uint64
	noNewPrivs bool
}

// CheckAgentWorker runs the checks 6, 7 and 8 of Spec 092, "The agent worker",
// "Start", in this order, and returns the error of the first check that fails:
//
//  6. cfg sets untrusted.uid and untrusted.gid.
//  7. The effective user ID of the process is 0, and its effective
//     capabilities include CAP_SETUID, CAP_SETGID, CAP_CHOWN, CAP_FOWNER,
//     CAP_DAC_OVERRIDE and CAP_KILL. The error names the first capability that
//     is absent.
//  8. The process has the flag no-new-privileges (NoNewPrivs: 1 in
//     /proc/self/status).
//
// The error starts with "--agent:" and names the key or the item, the expected
// value and the value. The worker stops with the exit code 1 when it returns
// an error.
func CheckAgentWorker(cfg config.UntrustedConfig) error {
	if !cfg.Enabled {
		return agentWorkerCheck(cfg, privileges{}, nil)
	}
	priv, err := processPrivileges()
	return agentWorkerCheck(cfg, priv, err)
}

// agentWorkerCheck runs the checks on the privileges that the process read.
func agentWorkerCheck(cfg config.UntrustedConfig, priv privileges, err error) error {
	if cfg.IsolationOff {
		return errors.New(`--agent: key "untrusted.isolation" is "off", not "on": an agent worker always has isolation`)
	}
	if !cfg.Enabled {
		return errors.New(`--agent: key "untrusted.uid" is not set: an agent worker needs the restricted user of the keys untrusted.uid and untrusted.gid`)
	}
	if err != nil {
		return fmt.Errorf("--agent: %w", err)
	}
	if err := priv.checkChange(); err != nil {
		return fmt.Errorf("--agent: %w: an agent worker needs the user ID 0 and the capabilities %s", err, capabilityNames())
	}
	if !priv.noNewPrivs {
		return errors.New("--agent: NoNewPrivs is 0 in /proc/self/status, not 1: an agent worker needs the flag no-new-privileges")
	}
	return nil
}

// checkChange checks that the process can change to the restricted user.
func (p privileges) checkChange() error {
	if p.euid != 0 {
		return fmt.Errorf("the effective user ID is %d, not 0", p.euid)
	}
	for _, c := range requiredCapabilities {
		if p.capEff&(1<<c.bit) == 0 {
			return fmt.Errorf("the effective capability %s is absent", c.name)
		}
	}
	return nil
}

func capabilityNames() string {
	names := make([]string, 0, len(requiredCapabilities))
	for _, c := range requiredCapabilities {
		names = append(names, c.name)
	}
	return strings.Join(names, ", ")
}
