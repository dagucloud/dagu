// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { stringify } from 'yaml';
import type { ActionCatalogEntry } from '../contracts';

/** Entries of one namespace, in catalog order. */
export interface PaletteGroup {
  namespace: string;
  entries: ActionCatalogEntry[];
}

/**
 * Keep the entries that match every word of the query.
 * A word matches the name, title, namespace or description, ignoring case.
 * An empty query returns all entries. The catalog order does not change.
 */
export function filterCatalog(
  catalog: ActionCatalogEntry[],
  query: string
): ActionCatalogEntry[] {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) {
    return catalog;
  }
  return catalog.filter((entry) => {
    const haystack = [
      entry.name,
      entry.title,
      entry.namespace,
      entry.description ?? '',
    ]
      .join('\n')
      .toLowerCase();
    return terms.every((term) => haystack.includes(term));
  });
}

function namespaceRank(namespace: string): number {
  if (namespace === 'core') {
    return 0;
  }
  return namespace === 'custom' ? 2 : 1;
}

/**
 * Group entries by namespace. The namespace "core" comes first and "custom"
 * comes last. Other namespaces are in alphabetical order.
 */
export function groupCatalog(entries: ActionCatalogEntry[]): PaletteGroup[] {
  const byNamespace = new Map<string, ActionCatalogEntry[]>();
  for (const entry of entries) {
    const list = byNamespace.get(entry.namespace);
    if (list) {
      list.push(entry);
    } else {
      byNamespace.set(entry.namespace, [entry]);
    }
  }
  return [...byNamespace.entries()]
    .sort(
      ([a], [b]) =>
        namespaceRank(a) - namespaceRank(b) || (a < b ? -1 : a > b ? 1 : 0)
    )
    .map(([namespace, list]) => ({ namespace, entries: list }));
}

/** The entries in the order that the user sees them. */
export function flattenGroups(groups: PaletteGroup[]): ActionCatalogEntry[] {
  return groups.flatMap((group) => group.entries);
}

/** Search words that find actions by other names: "curl" finds "http.request". */
export type AliasMap = Readonly<Record<string, readonly string[]>>;

/** How well an entry matches a query. A lower value comes first. */
export const MatchRank = {
  exactName: 0,
  namePrefix: 1,
  titleWord: 2,
  alias: 3,
  text: 4,
} as const;
export type MatchRank = (typeof MatchRank)[keyof typeof MatchRank];

function titleWords(title: string): string[] {
  return title
    .toLowerCase()
    .split(/[^\p{L}\p{N}]+/u)
    .filter(Boolean);
}

/**
 * The rank of an entry for a query, or null when it does not match. The rank
 * is the best of: the exact name, a name that starts with the query, title
 * words that start with each query word, an alias that starts with the query,
 * and the text match of `filterCatalog`.
 */
export function matchRank(
  entry: ActionCatalogEntry,
  query: string,
  aliases: AliasMap = {}
): MatchRank | null {
  const text = query.trim().toLowerCase();
  const terms = text.split(/\s+/).filter(Boolean);
  if (terms.length === 0) {
    return null;
  }
  const name = entry.name.toLowerCase();
  if (name === text) {
    return MatchRank.exactName;
  }
  if (name.startsWith(text)) {
    return MatchRank.namePrefix;
  }
  const words = titleWords(entry.title);
  if (terms.every((term) => words.some((word) => word.startsWith(term)))) {
    return MatchRank.titleWord;
  }
  if (aliasPosition(entry, text, aliases) !== null) {
    return MatchRank.alias;
  }
  return filterCatalog([entry], text).length > 0 ? MatchRank.text : null;
}

// The place of the entry in the targets of the first alias that the query
// starts, or null.
function aliasPosition(
  entry: ActionCatalogEntry,
  text: string,
  aliases: AliasMap
): number | null {
  for (const [alias, targets] of Object.entries(aliases)) {
    const position = targets.indexOf(entry.name);
    if (position >= 0 && alias.toLowerCase().startsWith(text)) {
      return position;
    }
  }
  return null;
}

/**
 * Keep the entries that match the query, best match first. Alias matches
 * follow the order of the alias targets. Other entries of the same rank keep
 * the catalog order. An empty query returns all entries.
 */
export function rankCatalog(
  catalog: ActionCatalogEntry[],
  query: string,
  aliases: AliasMap = {}
): ActionCatalogEntry[] {
  if (query.trim() === '') {
    return catalog;
  }
  return catalog
    .map((entry, index) => {
      const rank = matchRank(entry, query, aliases);
      const target =
        rank === MatchRank.alias
          ? (aliasPosition(entry, query.trim().toLowerCase(), aliases) ?? 0)
          : 0;
      return { entry, index, rank, target };
    })
    .filter(
      (item): item is typeof item & { rank: MatchRank } => item.rank !== null
    )
    .sort((a, b) => a.rank - b.rank || a.target - b.target || a.index - b.index)
    .map((item) => item.entry);
}

/**
 * Group ranked entries by namespace. The group of the best entry comes first,
 * and each group keeps the order of the entries.
 */
export function groupRanked(entries: ActionCatalogEntry[]): PaletteGroup[] {
  const groups = new Map<string, ActionCatalogEntry[]>();
  for (const entry of entries) {
    const list = groups.get(entry.namespace);
    if (list) {
      list.push(entry);
    } else {
      groups.set(entry.namespace, [entry]);
    }
  }
  return [...groups.entries()].map(([namespace, list]) => ({
    namespace,
    entries: list,
  }));
}

/** The keys that a new step of the entry must have: those of its template. */
export function requiredKeys(entry: ActionCatalogEntry): string[] {
  const { template } = entry;
  if (template.action === undefined) {
    return Object.keys(template).filter((key) => key !== 'name');
  }
  return Object.keys(template.with ?? {});
}

/** The YAML of the template of the entry: what a pick writes, without the name. */
export function templateYaml(entry: ActionCatalogEntry): string {
  return stringify(entry.template);
}
