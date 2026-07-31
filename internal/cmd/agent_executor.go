// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"golang.org/x/term"
)

// ExecuteAgent runs an agent with optional progress display and handles common execution logic
func ExecuteAgent(ctx *Context, agentInstance *agent.Agent, dag *core.DAG, dagRunID string, logFile *os.File) error {
	// Check if progress display should be enabled
	enableProgress := shouldEnableProgress(ctx)

	// Configure logger for progress display if needed
	if enableProgress {
		configureLoggerForProgress(ctx, logFile)
	} else {
		// Normal logging configuration
		ctx.LogToFile(logFile)
	}

	// Set up signal handling
	listenSignals(ctx, agentInstance)

	// Run the DAG
	if err := agentInstance.Run(ctx); err != nil {
		logger.Error(ctx, "Failed to execute dag-run",
			tag.DAG(dag.Name),
			tag.RunID(dagRunID),
			tag.Error(err),
		)
		if ctx.Proc != nil {
			_ = ctx.Proc.Stop(ctx)
		}
		if ctx.Quiet {
			os.Exit(1)
		} else {
			agentInstance.PrintSummary(ctx)
			// If progress display was enabled, exit directly without returning error
			// to avoid printing "exit status 1" which ruins the UI
			if enableProgress {
				os.Exit(1)
			}
			return fmt.Errorf("failed to execute the dag-run %s (dag-run ID: %s): %w",
				dag.Name, dagRunID, err)
		}
	}

	// Print summary if not quiet
	if !ctx.Quiet {
		agentInstance.PrintSummary(ctx)
	}

	return nil
}

// shouldEnableProgress checks if progress display should be enabled
func shouldEnableProgress(ctx *Context) bool {
	return !ctx.Quiet &&
		os.Getenv("DISABLE_PROGRESS") == "" &&
		isTerminal(os.Stderr)
}

// configureLoggerForProgress sets up the logger for progress display. It enables debug mode when ctx.Config.Core.Debug
// is true, applies ctx.Config.Core.LogFormat when set, and directs output to logFile when provided.
func configureLoggerForProgress(ctx *Context, logFile *os.File) {
	var opts []logger.Option
	if ctx.Config.Core.Debug {
		opts = append(opts, logger.WithDebug())
	}
	opts = append(opts, logger.WithQuiet()) // Suppress stderr output
	if ctx.Config.Core.LogFormat != "" {
		opts = append(opts, logger.WithFormat(ctx.Config.Core.LogFormat))
	}
	if logFile != nil {
		opts = append(opts, logger.WithWriter(logFile))
	}
	ctx.Context = logger.WithLogger(ctx.Context, logger.NewLogger(opts...))
}

// isTerminal checks if the given file is a terminal
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
