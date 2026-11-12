// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ActionCatalogEntry } from '../contracts';

/**
 * Filters the catalog with a free-text query. Each word of the query must
 * occur (case-insensitive) in the name, title, namespace or description of
 * an entry. Entries whose name starts with the query come first; the other
 * entries keep the catalog order. An empty query gives the whole catalog.
 */
export function searchCatalog(
  catalog: ActionCatalogEntry[],
  query: string
): ActionCatalogEntry[] {
  const text = query.trim().toLowerCase();
  if (!text) {
    return catalog;
  }
  const words = text.split(/\s+/);
  const matches = catalog.filter((entry) => {
    const haystack = [
      entry.name,
      entry.title,
      entry.namespace,
      entry.description ?? '',
    ]
      .join('\n')
      .toLowerCase();
    return words.every((word) => haystack.includes(word));
  });
  const first = matches.filter((entry) =>
    entry.name.toLowerCase().startsWith(text)
  );
  const rest = matches.filter(
    (entry) => !entry.name.toLowerCase().startsWith(text)
  );
  return [...first, ...rest];
}
