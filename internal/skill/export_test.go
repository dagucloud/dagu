// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

// SetBeforeHeldCheck sets fn to run right before a write checks that it still
// holds the lock of the skill.
func SetBeforeHeldCheck(s *Store, fn func()) { s.beforeHeldCheck = fn }
