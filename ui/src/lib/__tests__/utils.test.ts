// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { toMermaidNodeId } from '../utils';

describe('toMermaidNodeId', () => {
  it('always returns a prefixed Mermaid-safe ID', () => {
    expect(toMermaidNodeId('end')).toBe('node_65_6e_64');
    expect(toMermaidNodeId('extract (source)')).toBe(
      'node_65_78_74_72_61_63_74_20_28_73_6f_75_72_63_65_29'
    );
    expect(toMermaidNodeId('a🚀')).toBe('node_61_1f680');
    expect(toMermaidNodeId('')).toBe('node_empty');
  });

  it('does not collide raw encoded-looking names with names that need encoding', () => {
    expect(toMermaidNodeId('a-b')).not.toBe(toMermaidNodeId('au2d_b'));
  });
});
