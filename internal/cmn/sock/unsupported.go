// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sock

import (
	"errors"
	"fmt"
)

// ErrUnsupported indicates the platform/runtime cannot provide Unix sockets.
var ErrUnsupported = errors.New("unix socket transport unsupported")

func wrapListenError(err error) error {
	if err == nil {
		return nil
	}
	if isUnsupportedListenError(err) {
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	return err
}
