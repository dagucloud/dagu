// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"fmt"
	"strings"
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
