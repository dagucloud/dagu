// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

func validateStartArgumentSeparator(ctx *Context, args []string) error {
	return spec.ValidateStartArgs(ctx.Command.ArgsLenAtDash() != -1, args)
}

func validateStartPositionalParamCount(ctx *Context, args []string, dag *ir.DAG) error {
	input, err := buildStartValidationInput(ctx, args)
	if err != nil {
		return err
	}
	return spec.ValidateStartParams(dag.DefaultParams, input)
}

func buildStartValidationInput(ctx *Context, args []string) (spec.StartParamInput, error) {
	if argsLenAtDash := ctx.Command.ArgsLenAtDash(); argsLenAtDash != -1 {
		if argsLenAtDash >= len(args) {
			return spec.StartParamInput{}, nil
		}
		return spec.StartParamInput{DashArgs: quoteStartDashArgs(args[argsLenAtDash:])}, nil
	}

	raw, err := ctx.Command.Flags().GetString("params")
	if err != nil {
		return spec.StartParamInput{}, fmt.Errorf("failed to get parameters: %w", err)
	}
	return spec.StartParamInput{RawParams: raw}, nil
}
