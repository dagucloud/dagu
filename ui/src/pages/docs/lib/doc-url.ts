// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export function normalizeDocPathFromURL(path: string): string {
  return path.replace(/\.md$/i, '');
}
