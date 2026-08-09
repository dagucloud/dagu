// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export function encodeWikiPagePathForURL(wikiPagePath: string): string {
  const unambiguousPath = wikiPagePath.toLowerCase().endsWith('.md')
    ? `${wikiPagePath}.md`
    : wikiPagePath;
  return unambiguousPath.split('/').map(encodeURIComponent).join('/');
}
