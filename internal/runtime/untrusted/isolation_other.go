// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux

package untrusted

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// errNeedsLinux is the error of a check on another system.
var errNeedsLinux = errors.New("the change to the restricted user needs Linux")

// isolation is not available on this system. A configuration with a restricted
// user makes each untrusted step fail.
type isolation struct{ home string }

// isolate fails: the restricted user exists on Linux only.
func isolate(_ context.Context, req Request, _ *Step) (*Step, error) {
	return nil, fmt.Errorf("untrusted step %q: %w: the configuration sets untrusted.uid", req.StepName, errNeedsLinux)
}

func (*isolation) end(context.Context) error { return nil }

func (*isolation) started(int) {}

func (*isolation) abandon() {}

func (*isolation) prepare(*exec.Cmd, []string) (func(), error) { return nil, errNeedsLinux }

func (*isolation) setOutputTarget(target string) string { return target }

func (*isolation) shareReadOnly(string) error { return errNeedsLinux }

// Poisoned reports no failed end: a process outside Linux has no isolation.
func Poisoned() error { return nil }

// writableBy reports no folder: a process outside Linux has no restricted user.
func writableBy(string, uint32, uint32) bool { return false }
