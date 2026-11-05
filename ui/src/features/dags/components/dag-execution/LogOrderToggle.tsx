// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowDown } from 'lucide-react';
import React from 'react';
import { Button } from '@/components/ui/button';
import { useUserPreferences } from '@/contexts/UserPreference';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * The order of the lines of a log viewer, from the user preference: the
 * oldest line at the top ('oldToNew') or the newest line at the top
 * ('newToOld').
 */
export function useLogOrder(): {
  newestFirst: boolean;
  toggle: () => void;
} {
  const { preferences, updatePreference } = useUserPreferences();
  const newestFirst = preferences.logOrder === 'newToOld';
  return {
    newestFirst,
    toggle: () =>
      updatePreference('logOrder', newestFirst ? 'oldToNew' : 'newToOld'),
  };
}

/** The index in the loaded lines of the line that the viewer draws at `row`. */
export function lineAt(row: number, count: number, newestFirst: boolean) {
  return newestFirst ? count - 1 - row : row;
}

/** The keys that read away from the live end: they pause the follow. */
export function awayKeys(newestFirst: boolean): string[] {
  return newestFirst
    ? ['ArrowDown', 'PageDown', 'End']
    : ['ArrowUp', 'PageUp', 'Home'];
}

/**
 * The distance in px between the scroll position and the live end of the
 * viewer: its bottom when the oldest line is at the top, its top when the
 * newest line is.
 */
export function distanceFromLiveEnd(
  element: Pick<HTMLElement, 'scrollHeight' | 'clientHeight' | 'scrollTop'>,
  newestFirst: boolean
): number {
  return newestFirst
    ? element.scrollTop
    : element.scrollHeight - element.clientHeight - element.scrollTop;
}

/**
 * The button that reverses the order of the lines. It names the current
 * order; the arrow points down for old to new (the log reads downward) and
 * turns up for new to old. The label sits in a slot of fixed width, so the
 * toolbar does not move when the order changes. In a toolbar narrower than
 * 25rem (@container/logbar) only the arrow shows, so the row fits a phone.
 */
export function LogOrderToggle({
  newestFirst,
  onToggle,
}: {
  newestFirst: boolean;
  onToggle: () => void;
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <Button
      size="xs"
      variant="outline"
      data-testid="log-order"
      data-order={newestFirst ? 'newToOld' : 'oldToNew'}
      title={ts('Reverse the order of the lines')}
      onClick={onToggle}
      className="w-28 shrink-0 justify-between whitespace-nowrap @max-[25rem]/logbar:w-auto"
    >
      <span className="truncate @max-[25rem]/logbar:sr-only">
        {newestFirst ? ts('New to old') : ts('Old to new')}
      </span>
      <ArrowDown
        aria-hidden="true"
        className={`h-3.5 w-3.5 shrink-0 transition-transform motion-reduce:transition-none ${newestFirst ? 'rotate-180' : ''}`}
      />
    </Button>
  );
}
