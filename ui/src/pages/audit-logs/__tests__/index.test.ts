// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';

import { getQuickFilterValue } from '..';

describe('getQuickFilterValue', () => {
  it.each([
    ['mcp', 'mcp', 'mcp', 'all', 'mcp'],
    ['all', 'rest', 'rest_api', 'all', 'rest'],
    ['all', 'all', 'all', 'failed', 'failed'],
    ['all', 'all', 'all', 'denied', 'denied'],
    ['all', 'all', 'all', 'all', 'all'],
  ])(
    'derives %s/%s/%s/%s as %s',
    (category, source, surface, result, expected) => {
      expect(getQuickFilterValue(category, source, surface, result)).toBe(
        expected
      );
    }
  );
});
