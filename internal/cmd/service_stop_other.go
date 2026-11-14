// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package cmd

import (
	"errors"
	"os"
)

// serviceStopRequests delivers service-stop requests, which only the Windows
// service uses; elsewhere it never delivers.
func serviceStopRequests(string) (<-chan os.Signal, error) {
	return nil, nil
}

func requestServiceStop(string) error {
	return errors.New("service-stop is only used by the Windows service; send SIGTERM instead")
}
