// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';

/**
 * How many nodes a retry runs again: those whose run failed, was aborted, or
 * never started (RFC 013 §Retry).
 */
export function nodesToRetry(
  counts: components['schemas']['NodeRunCounts']
): number {
  return counts.failed + counts.aborted + counts.notStarted;
}
