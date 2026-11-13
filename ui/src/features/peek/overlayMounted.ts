// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Tells if the app mounts the workflow overlay of the peek. The app does not
 * mount it: a click on a row of a list opens the page of the workflow. With
 * no overlay, PeekHost gives no controller, and each list and PeekLink
 * falls back to the full page. Set it to true to mount the overlay again.
 */
export const OVERLAY_MOUNTED = false;
