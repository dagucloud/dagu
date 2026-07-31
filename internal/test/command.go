// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// CmdTest is a helper struct to test commands.
type CmdTest struct {
	Name        string   // Name of the test.
	Args        []string // Arguments to pass to the command.
	ExpectedOut []string // Expected output to be present in the standard output / error.
}

// Command is a helper struct to test commands.
type Command struct {
	Helper
}

func (th Command) RunCommand(t *testing.T, cmd *cobra.Command, testCase CmdTest) {
	t.Helper()
	require.NoError(t, th.ExecuteCommand(cmd, testCase))
}

// RunCommandWithError runs a command and returns the error (if any) without failing the test.
func (th Command) RunCommandWithError(t *testing.T, cmd *cobra.Command, testCase CmdTest) error {
	t.Helper()
	return th.ExecuteCommand(cmd, testCase)
}

// ExecuteCommand runs a command and validates the expected output without touching testing.T.
// It is safe to use from background goroutines in tests.
func (th Command) ExecuteCommand(cmd *cobra.Command, testCase CmdTest) error {
	cmdRoot := &cobra.Command{Use: "root"}
	cmdRoot.AddCommand(cmd)

	// Set arguments.
	cmdRoot.SetArgs(WithConfigFlag(testCase.Args, th.Config))

	// Run the command
	err := cmdRoot.ExecuteContext(th.Context)
	if err != nil {
		return err
	}

	output := th.LoggingOutput.String()
	for _, expectedOutput := range testCase.ExpectedOut {
		if len(expectedOutput) > 0 && !strings.Contains(output, expectedOutput) {
			return fmt.Errorf("expected output %q not found in command output", expectedOutput)
		}
	}
	return nil
}

func SetupCommand(t *testing.T, opts ...HelperOption) Command {
	t.Helper()

	opts = append(opts, WithCaptureLoggingOutput())
	return Command{Helper: Setup(t, opts...)}
}

// WithConfigFlag appends --config <file> unless already present.
func WithConfigFlag(args []string, cfg *config.Config) []string {
	if cfg == nil || cfg.Paths.ConfigFileUsed == "" {
		return args
	}
	for i := range args {
		arg := args[i]
		if arg == "--config" || arg == "-c" || hasConfigInline(arg) {
			return args
		}
		if args[i] == "--" {
			// Insert config flag before passthrough args so it isn't treated as a DAG param.
			withFlag := append([]string{}, args[:i]...)
			withFlag = append(withFlag, "--config", cfg.Paths.ConfigFileUsed)
			withFlag = append(withFlag, args[i:]...)
			return withFlag
		}
	}
	return append(args, "--config", cfg.Paths.ConfigFileUsed)
}

func hasConfigInline(arg string) bool {
	return strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "-c=")
}

// CreateDAGFile creates a DAG file in the DAGsDir for command tests
func (c Command) CreateDAGFile(t *testing.T, name string, content string) string {
	t.Helper()

	dagFile := filepath.Join(c.Config.Paths.DAGsDir, name)
	// Create the directory if it doesn't exist
	err := os.MkdirAll(filepath.Dir(dagFile), 0750)
	require.NoError(t, err)
	// Write the DAG file
	err = os.WriteFile(dagFile, []byte(content), 0600)
	require.NoError(t, err)
	return dagFile
}
