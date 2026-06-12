// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"

	"github.com/dagucloud/dagu/internal/core/v3schema"
	"github.com/spf13/cobra"
)

// Workflow creates the 'workflow' CLI command for v3 workflow files.
func Workflow() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Manage v3 workflow files",
	}
	cmd.AddCommand(workflowValidate())
	return cmd
}

func workflowValidate() *cobra.Command {
	return &cobra.Command{
		Use:          "validate <workflow_file>",
		Short:        "Validate a v3 workflow YAML file",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read workflow file %s: %w", args[0], err)
			}
			if err := v3schema.ValidateWorkflow(data); err != nil {
				return fmt.Errorf("workflow validation failed for %s: %w", args[0], err)
			}
			return nil
		},
	}
}
