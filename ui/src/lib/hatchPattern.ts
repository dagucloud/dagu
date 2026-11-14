// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * One layer of CSS background that hatches a bar: diagonal lines in the page
 * colour over the colour of the bar, which stays as it is. The run timeline
 * uses it for a test run, and the step timeline for the time of a step that a
 * run reused from another run.
 */
export const HATCH_PATTERN =
  'repeating-linear-gradient(135deg, var(--background) 0 1.5px, transparent 1.5px 4px)';
