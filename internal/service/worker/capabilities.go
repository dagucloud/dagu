// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// detectCapabilities runs the self-tests of the worker and returns the
// capabilities that pass (Spec 094, "Version check"). A test that fails logs its
// error, and the worker does not announce its capability.
func detectCapabilities(ctx context.Context) []string {
	var capabilities []string
	if err := aiRelaySelfTest(); err != nil {
		logger.Error(ctx, "The worker cannot run the AI relay and does not announce the capability",
			slog.String("capability", ir.CapabilityAIRelay), slog.String("error", err.Error()))
	} else {
		capabilities = append(capabilities, ir.CapabilityAIRelay)
	}
	return capabilities
}

// aiRelaySelfTest binds a listener on 127.0.0.1, as the relay does for each
// attempt.
func aiRelaySelfTest() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("the self-test cannot bind a listener on 127.0.0.1: %w", err)
	}
	return listener.Close()
}
