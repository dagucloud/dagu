// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/spf13/cobra"
)

const peerInitOutFlag = "out"

// Peer creates the 'peer' command group for peer transport setup.
func Peer() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peer",
		Short: "Prepare peer transport settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(peerInitCommand())
	return cmd
}

func peerInitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init --out <dir>",
		Short: "Write a peer CA and a peer token before the coordinator exists",
		Long: `Write ca.crt, ca.key, and token into <dir>, each readable by its user
only. <dir> is created when it is missing.

ca.crt and ca.key are a CA as the coordinator generates one. token holds a
peer token. Give the coordinator ca.crt as peer.ca_cert_file, ca.key as
peer.ca_key_file, and the content of token as the value of peer.token or
DAGU_PEER_TOKEN. Give other peer processes ca.crt and the content of token.

The command reads no config file and needs no running Dagu. When any of the
three files exists, it writes none and exits with code 1.`,
		Example:      `  dagu peer init --out ./peer`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         runPeerInit,
	}
	cmd.Flags().String(peerInitOutFlag, "", "Directory to write ca.crt, ca.key, and token into")
	_ = cmd.MarkFlagRequired(peerInitOutFlag)
	return cmd
}

func runPeerInit(cmd *cobra.Command, _ []string) error {
	dir, err := cmd.Flags().GetString(peerInitOutFlag)
	if err != nil {
		return err
	}
	paths, err := coordinator.WritePeerInit(dir)
	if existing, ok := errors.AsType[*fs.PathError](err); ok && errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s exists; dagu peer init writes no file over an existing one", existing.Path)
	}
	if err != nil {
		return err
	}
	for _, path := range paths {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), path)
	}
	return nil
}
