// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export function encodeWikiPagePathForURL(wikiPagePath: string): string {
  return wikiPagePath.split('/').map(encodeURIComponent).join('/');
}
