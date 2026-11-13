// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { SUB_ICON_SIZE } from '../subRow';
import { leafStub } from '../nodes/SubLeaf';

describe('leafStub', () => {
  it('joins the entry on the side of a round node to the edge of its icon', () => {
    const half = SUB_ICON_SIZE / 2;
    // The node is on the left of its step: the entry is on its right side, and
    // the stub runs from the right edge of the icon to that side.
    expect(leafStub('left')).toEqual({
      top: half,
      left: `calc(50% + ${half}px)`,
      right: '0px',
    });
    // The node is on the right of its step: the stub runs from the left side to the icon.
    expect(leafStub('right')).toEqual({
      top: half,
      left: '0px',
      right: `calc(50% + ${half}px)`,
    });
  });
});
