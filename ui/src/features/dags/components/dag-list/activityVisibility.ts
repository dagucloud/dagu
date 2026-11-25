// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext } from 'react';
import { PanelWidthContext } from '@/components/SplitLayout';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { WIDE_QUERY } from '@/lib/breakpoints';

// Threshold in pixels below which we switch to card view
// Set higher than table's comfortable minimum width (~700px for all columns)
export const CARD_VIEW_THRESHOLD = 700;
// The strip of the activity needs room: below this width of the table, or below
// the xl breakpoint of the page, the table leaves the column out.
export const ACTIVITY_COLUMN_MIN_WIDTH = 1100;
/** The xl breakpoint of Tailwind, from which the page shows the column. */
const ACTIVITY_COLUMN_QUERY = '(min-width: 1280px)';

/**
 * Whether the Workflows list shows the activity of its rows: in the cards, or
 * in the Activity column of the table. A table too narrow for the column
 * shows no activity at all.
 */
export function useListShowsActivity(): boolean {
  const panelWidth = useContext(PanelWidthContext);
  const phone = !useMediaQuery(WIDE_QUERY, true);
  const columnFits = useMediaQuery(ACTIVITY_COLUMN_QUERY, true);
  // A phone shows the cards at any width of the panel.
  if (phone) return true;
  return panelWidth === null
    ? columnFits
    : panelWidth < CARD_VIEW_THRESHOLD ||
        panelWidth >= ACTIVITY_COLUMN_MIN_WIDTH;
}
