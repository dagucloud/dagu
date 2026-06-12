// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
)

func validateStartArgumentSeparator(ctx *Context, args []string) error {
	return core.ValidateStartArgs(ctx.Command.ArgsLenAtDash() != -1, args)
}

func validateStartPositionalParamCount(ctx *Context, args []string, dag *core.DAG) error {
	input, err := buildStartValidationInput(ctx, args)
	if err != nil {
		return err
	}
	return core.ValidateStartParams(dag.DefaultParams, input)
}

func buildStartValidationInput(ctx *Context, args []string) (core.StartParamInput, error) {
	if argsLenAtDash := ctx.Command.ArgsLenAtDash(); argsLenAtDash != -1 {
		if argsLenAtDash >= len(args) {
			return core.StartParamInput{}, nil
		}
		return core.StartParamInput{DashArgs: quoteStartDashArgs(args[argsLenAtDash:])}, nil
	}

	raw, err := ctx.Command.Flags().GetString("params")
	if err != nil {
		return core.StartParamInput{}, fmt.Errorf("failed to get parameters: %w", err)
	}
	return core.StartParamInput{RawParams: raw}, nil
}
