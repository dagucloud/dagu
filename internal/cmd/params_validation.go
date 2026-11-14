// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

func validateStartArgumentSeparator(ctx *Context, args []string) error {
	return spec.ValidateStartArgs(ctx.Command.ArgsLenAtDash() != -1, args)
}

// rawParams is the params text resolved by loadDAGWithParams ("--params" flag
// value or piped stdin content); it is ignored when params come after "--".
func validateStartPositionalParamCount(ctx *Context, args []string, dag *ir.DAG, rawParams string) error {
	return spec.ValidateStartParams(dag.DefaultParams, buildStartValidationInput(ctx, args, rawParams))
}

func buildStartValidationInput(ctx *Context, args []string, rawParams string) spec.StartParamInput {
	if argsLenAtDash := ctx.Command.ArgsLenAtDash(); argsLenAtDash != -1 {
		if argsLenAtDash >= len(args) {
			return spec.StartParamInput{}
		}
		return spec.StartParamInput{DashArgs: quoteStartDashArgs(args[argsLenAtDash:])}
	}

	if ctx.Command.Flags().Changed("params") {
		rawParams = stringutil.RemoveQuotes(rawParams)
	}
	return spec.StartParamInput{RawParams: rawParams}
}
