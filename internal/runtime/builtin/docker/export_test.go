// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

func ExecCommandForTest(shell, cmd []string, opts ExecOptions) []string {
	return execCommand(shell, cmd, opts)
}

func MergeEnvByKeyForTest(layers ...[]string) []string {
	return mergeEnvByKey(layers...)
}
