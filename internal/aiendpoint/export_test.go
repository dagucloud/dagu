// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import "time"

// SetClock replaces the clock of the models reads. Tests only.
func (s *Service) SetClock(now func() time.Time) { s.now = now }
