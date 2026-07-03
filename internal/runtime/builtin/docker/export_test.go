// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import specs "github.com/opencontainers/image-spec/specs-go/v1"

func ExecCommandForTest(shell, cmd []string, opts ExecOptions) []string {
	return execCommand(shell, cmd, opts)
}

func MergeEnvByKeyForTest(layers ...[]string) []string {
	return mergeEnvByKey(layers...)
}

func ImagePlatformMatchesForTest(target specs.Platform, image specs.Platform) bool {
	return imagePlatformMatches(target, image)
}
