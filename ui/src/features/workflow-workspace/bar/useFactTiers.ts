// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useLayoutEffect, useState } from 'react';
import type { InlineFact } from './RunFacts';

/**
 * The narrowest bar, in px, that has room for each fact of the run row. The
 * facts go in this order when the bar narrows: the trigger first, then the
 * start, then the duration. The ID is the last: under its width the row is
 * gone and the tooltip of the status icon holds the ID. The widths come from
 * the measured text of each fact (e2e/workspace/run-header.spec.ts).
 */
export const FACT_MIN_BAR_PX: Readonly<Record<InlineFact, number>> = {
  id: 1152,
  duration: 1152,
  started: 1216,
  trigger: 1280,
};

/** The same widths for a failed run: its failed step takes the room of the trigger. */
export const FACT_MIN_BAR_PX_FAILED: Readonly<Record<InlineFact, number>> = {
  id: 1152,
  duration: 1216,
  started: 1280,
  trigger: Number.POSITIVE_INFINITY,
};

/** The facts that a bar of this width has room for. */
export function factsShownAt(
  barWidth: number,
  failed: boolean
): ReadonlySet<InlineFact> {
  const mins = failed ? FACT_MIN_BAR_PX_FAILED : FACT_MIN_BAR_PX;
  return new Set(
    (Object.keys(mins) as InlineFact[]).filter((fact) => barWidth >= mins[fact])
  );
}

const BAR = '[data-testid="ww-bar"]';

/**
 * The width of the workflow bar around the element, kept current. 0 while it
 * is unknown. The bar is the one container that all the room decisions of
 * the run chip read.
 */
export function useBarWidth(element: HTMLElement | null): number {
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const bar = element?.closest(BAR);
    if (!bar) return;
    const read = () => setWidth(bar.getBoundingClientRect().width);
    read();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(read);
    observer.observe(bar);
    return () => observer.disconnect();
  }, [element]);
  return width;
}
