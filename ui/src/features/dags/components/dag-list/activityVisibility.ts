// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext } from 'react';
import { PanelWidthContext } from '@/components/SplitLayout';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { WIDE_QUERY } from '@/lib/breakpoints';

// The width of the panel below which the list shows cards, above the
// comfortable minimum width of the table with all its columns (~700px).
export const CARD_VIEW_THRESHOLD = 700;
// The strip of the activity needs room: below this width of the table, or below
// the xl breakpoint of the page, the table leaves the column out.
const ACTIVITY_COLUMN_MIN_WIDTH = 1100;
/** The xl breakpoint of Tailwind, from which the page shows the column. */
const ACTIVITY_COLUMN_QUERY = '(min-width: 1280px)';

/** Where the Workflows list shows the activity of its rows. */
export interface ActivityPlacement {
  /** The cards show, each with its strip. */
  cards: boolean;
  /** The table shows, with its Activity column. */
  column: boolean;
}

/**
 * Where the Workflows list shows the activity of its rows: in the cards, in
 * the Activity column of the table, or nowhere when the table is too narrow
 * for the column.
 */
export function useActivityPlacement(): ActivityPlacement {
  const panelWidth = useContext(PanelWidthContext);
  const phone = !useMediaQuery(WIDE_QUERY, true);
  const columnFits = useMediaQuery(ACTIVITY_COLUMN_QUERY, true);
  // A phone shows the cards at any width of the panel.
  const cards =
    phone || (panelWidth !== null && panelWidth < CARD_VIEW_THRESHOLD);
  const column =
    !cards &&
    (panelWidth === null
      ? columnFits
      : panelWidth >= ACTIVITY_COLUMN_MIN_WIDTH);
  return { cards, column };
}

/** The class names of a cell and a column of the table. */
export interface ColumnClasses {
  cell: string;
  col: string;
}

/**
 * The classes of the Activity column of the table, by the rule of
 * useActivityPlacement. Without a panel, the xl classes of the page apply it,
 * so that the first paint does not wait for the media query. The table leaves
 * the column out when it has no activity to show. Whole class names, so that
 * the style sheet keeps them.
 */
export function activityColumnClasses(
  panelWidth: number | null,
  hasActivity: boolean
): ColumnClasses {
  if (
    !hasActivity ||
    (panelWidth !== null && panelWidth < ACTIVITY_COLUMN_MIN_WIDTH)
  ) {
    return { cell: 'hidden', col: 'hidden' };
  }
  if (panelWidth === null) {
    return { cell: 'hidden xl:table-cell', col: 'hidden xl:table-column' };
  }
  return { cell: '', col: '' };
}

/** Whether the Workflows list shows the activity of its rows at all. */
export function useListShowsActivity(): boolean {
  const { cards, column } = useActivityPlacement();
  return cards || column;
}
