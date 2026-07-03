// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import "github.com/go-git/go-git/v5/plumbing/transport"

func AuthForTest(raw map[string]any, workDir string) (transport.AuthMethod, error) {
	cfg := config{}
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, err
	}
	return (&executorImpl{cfg: cfg, workDir: workDir}).auth()
}
