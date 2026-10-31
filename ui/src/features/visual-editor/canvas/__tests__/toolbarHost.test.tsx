// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  TOOLBAR_GROUP_CLASS,
  TOOLBAR_ROW_CLASS,
  TOOLBAR_ZONE_CLASS,
} from '../toolbarHost';

const tokens = (value: string) => value.split(/\s+/);

// The row of the toolbars floats over the graph: it has no background, it
// lets a click through, and each group takes the pointer again.
describe('toolbar row of a graph zone', () => {
  it('floats over the top of the zone with no background and no pointer', () => {
    const row = tokens(TOOLBAR_ROW_CLASS);
    expect(row).toEqual(
      expect.arrayContaining([
        'absolute',
        'inset-x-0',
        'top-0',
        'pointer-events-none',
        'h-10',
      ])
    );
    for (const token of row) {
      expect(token).not.toMatch(/^(bg-|border|shadow)/);
    }
  });

  it('keeps the height of the row for the panels', () => {
    expect(TOOLBAR_ZONE_CLASS).toContain('[--ww-toolbar-row:2.5rem]');
    expect(TOOLBAR_ZONE_CLASS).toContain('max-md:[--ww-toolbar-row:0px]');
  });

  it('gives the pointer back to a group and a floating chrome to its buttons', () => {
    const group = tokens(TOOLBAR_GROUP_CLASS);
    expect(group).toContain('pointer-events-auto');
    // No background on the group itself; only on its buttons.
    expect(group.filter((token) => /^bg-|^border|^shadow/.test(token))).toEqual(
      []
    );
    expect(group.some((token) => token.endsWith(':bg-card/90'))).toBe(true);
    // The canvas moves under the row: no blur to repaint.
    expect(group.some((token) => token.includes('backdrop-blur'))).toBe(false);
  });
});
