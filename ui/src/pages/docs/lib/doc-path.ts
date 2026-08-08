// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export function encodeDocPathForURL(docPath: string): string {
  return docPath.split('/').map(encodeURIComponent).join('/');
}
