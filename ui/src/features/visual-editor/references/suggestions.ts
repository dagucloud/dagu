// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** The most suggestions that the field shows at once. The user types to narrow them. */
export const MAX_SUGGESTIONS = 50;

/**
 * The suggestions for the text of a field: the names that contain the text,
 * in the order of the list. A text with a `${` reference gets none, because
 * the reference picker owns it. A list that holds only the text itself gets
 * none either: there is nothing left to suggest.
 */
export function filterSuggestions(
  suggestions: readonly string[] | undefined,
  text: string
): string[] {
  if (!suggestions || suggestions.length === 0 || text.includes('${')) {
    return [];
  }
  const needle = text.trim().toLowerCase();
  const found = suggestions.filter((name) =>
    name.toLowerCase().includes(needle)
  );
  if (found.length === 1 && found[0] === text) return [];
  return found.slice(0, MAX_SUGGESTIONS);
}
