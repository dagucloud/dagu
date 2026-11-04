// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin || windows

package desktop

import (
	"slices"
	"strings"
	"time"
)

// windowCaptureWait bounds how long a capture waits for one window's
// picture, so a window whose application stops answering cannot hold the
// step. Windows reports such an application hung after about as long, and
// later captures leave its windows out.
const windowCaptureWait = 5 * time.Second

// appIn reports whether app is one of apps, compared ignoring case.
func appIn(app string, apps []string) bool {
	return app != "" && slices.ContainsFunc(apps, func(a string) bool { return strings.EqualFold(a, app) })
}
