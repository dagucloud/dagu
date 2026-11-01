// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package publicwebhook

import "time"

// SetTimingForTest shortens how the service paces itself.
func (s *Service) SetTimingForTest(check, backoff, refused time.Duration) {
	s.timing = timing{check: check, minBackoff: backoff, maxBackoff: backoff, stable: time.Hour, refused: refused}
}
