// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// errNoIsolation is the error of an untrusted step that needs the restricted
// user.
var errNoIsolation = errors.New("isolation with a restricted user is not available in this version of Dagu")

// isolation is not available. A configuration with a restricted user makes
// each untrusted step fail.
type isolation struct{ home string }

// isolate fails: this version of Dagu cannot run a step as the restricted user.
func isolate(_ context.Context, req Request, _ *Step) (*Step, error) {
	return nil, fmt.Errorf("untrusted step %q: %w", req.StepName, errNoIsolation)
}

func (*isolation) end(context.Context) error { return nil }

func (*isolation) started(int) {}

func (*isolation) abandon() {}

func (*isolation) prepare(*exec.Cmd, []string) error { return errNoIsolation }

func (*isolation) setOutputTarget(target string) string { return target }

// Poisoned reports no failed end: no step runs as the restricted user.
func Poisoned() error { return nil }
