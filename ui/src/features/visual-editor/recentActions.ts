// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

const STORAGE_KEY = 'dagu.visualEditor.recentActions';
const MAX_RECENT = 6;

/** The names of the actions that the user added lately, newest first. */
export function readRecentActions(): string[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    const value: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(value)
      ? value.filter((item): item is string => typeof item === 'string')
      : [];
  } catch {
    return [];
  }
}

/** The list with `name` first, once, and at most six names. */
export function withRecentAction(list: string[], name: string): string[] {
  return [name, ...list.filter((item) => item !== name)].slice(0, MAX_RECENT);
}

/** Keeps the list for the next visit. A browser without storage loses it. */
export function writeRecentActions(list: string[]): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(list));
  } catch {
    // The list is a convenience: the editor works without it.
  }
}

const FAVOURITES_KEY = 'dagu.visualEditor.favouriteActions';

/** The names of the actions that the user marked with a star, in star order. */
export function readFavouriteActions(): string[] {
  try {
    const raw = localStorage.getItem(FAVOURITES_KEY);
    const value: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(value)
      ? [
          ...new Set(
            value.filter((item): item is string => typeof item === 'string')
          ),
        ]
      : [];
  } catch {
    return [];
  }
}

/** The list with `name` removed when it is in it, else added at the end. */
export function toggleFavouriteAction(list: string[], name: string): string[] {
  return list.includes(name)
    ? list.filter((item) => item !== name)
    : [...list, name];
}

/** Keeps the favourites for the next visit. A browser without storage loses them. */
export function writeFavouriteActions(list: string[]): void {
  try {
    localStorage.setItem(FAVOURITES_KEY, JSON.stringify(list));
  } catch {
    // The favourites are a convenience: the editor works without them.
  }
}
