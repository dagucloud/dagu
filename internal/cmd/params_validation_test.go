// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestValidateStartArgumentSeparator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cliArgs []string
		wantErr string
	}{
		{
			name:    "NoExtraArgs",
			cliArgs: []string{"dag.yaml"},
		},
		{
			name:    "WithDashSeparator",
			cliArgs: []string{"dag.yaml", "--", "p1"},
		},
		{
			name:    "ExtraArgsWithoutDash",
			cliArgs: []string{"dag.yaml", "p1"},
			wantErr: "use '--' before parameters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, args := testValidationContext(t, tt.cliArgs)
			err := validateStartArgumentSeparator(ctx, args)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateStartPositionalParamCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cliArgs     []string
		defaultArgs string
		wantErr     string
	}{
		{
			name:        "NoDeclaredParamsAllowsNamedPairsAfterDash",
			cliArgs:     []string{"dag.yaml", "--", "key1=value1", "key2=value2"},
			defaultArgs: "",
		},
		{
			name:        "NoDeclaredParamsAllowsPositionalAfterDash",
			cliArgs:     []string{"dag.yaml", "--", "success"},
			defaultArgs: "",
		},
		{
			name:        "AllowsFewerThanDeclaredPositionalAfterDash",
			cliArgs:     []string{"dag.yaml", "--", "only-one"},
			defaultArgs: `p1 p2`,
		},
		{
			name:        "RejectsTooManyPositionalAfterDash",
			cliArgs:     []string{"dag.yaml", "--", "one", "two", "three"},
			defaultArgs: `p1 p2`,
			wantErr:     "too many positional params: expected at most 2, got 3",
		},
		{
			name:        "NamedOnlyDoesNotTriggerPositionalValidation",
			cliArgs:     []string{"--params", "KEY1=value1 KEY2=value2", "dag.yaml"},
			defaultArgs: `p1 p2`,
		},
		{
			name:        "NamedDefaultsWithNamedParamsDoNotTriggerPositionalValidation",
			cliArgs:     []string{"--params", "KEY1=value1 KEY2=value2", "dag.yaml"},
			defaultArgs: `KEY1=default1 KEY2=default2`,
		},
		{
			name:        "JSONAfterDashSkipsPositionalValidation",
			cliArgs:     []string{"dag.yaml", "--", `{"REGION":"us-east","VERSION":"2.0"}`},
			defaultArgs: `REGION=us-east-1 VERSION=1.0.0`,
		},
		{
			name:        "JSONInParamsFlagSkipsPositionalValidation",
			cliArgs:     []string{"--params", `{"KEY":"value"}`, "dag.yaml"},
			defaultArgs: `p1 p2`,
		},
		{
			name:        "NamedDeclaredParamAllowsSinglePositional",
			cliArgs:     []string{"dag.yaml", "--", "server1"},
			defaultArgs: `ITEM=default`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, args := testValidationContext(t, tt.cliArgs)
			dag := &core.DAG{DefaultParams: tt.defaultArgs}
			err := validateStartPositionalParamCount(ctx, args, dag)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func testValidationContext(t *testing.T, cliArgs []string) (*Context, []string) {
	t.Helper()

	command := &cobra.Command{Use: "start"}
	command.Flags().String("params", "", "")

	err := command.Flags().Parse(cliArgs)
	require.NoError(t, err)

	return &Context{Command: command}, command.Flags().Args()
}
