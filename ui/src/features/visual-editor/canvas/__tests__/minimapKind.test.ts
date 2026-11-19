// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { FlowNode } from '../engine';
import { minimapKindOf } from '../minimapKind';

function node(type: string, data: Record<string, unknown> = {}): FlowNode {
  return {
    id: type,
    type,
    position: { x: 0, y: 0 },
    width: 1,
    height: 1,
    data,
  };
}

describe('minimapKindOf', () => {
  it('finds an agent step by its facts, and a chat step by its model', () => {
    expect(
      minimapKindOf(node('step', { node: { agent: { chipRows: 0 } } }))
    ).toBe('agent');
    expect(
      minimapKindOf(node('step', { node: { chat: { models: [] } } }))
    ).toBe('agent');
    expect(minimapKindOf(node('agent'))).toBe('agent');
  });

  it('leaves a plain step, a router and a handler plain', () => {
    expect(
      minimapKindOf(node('step', { node: { label: 'a' } }))
    ).toBeUndefined();
    expect(minimapKindOf(node('step'))).toBeUndefined();
    expect(minimapKindOf(node('router'))).toBeUndefined();
    expect(minimapKindOf(node('handler'))).toBeUndefined();
  });

  it('draws the cards that hang from a step as its satellites', () => {
    for (const type of ['modelsat', 'promptsat', 'slotcard', 'dagcard']) {
      expect(minimapKindOf(node(type))).toBe('satellite');
    }
  });

  it('does not draw the "+" of an empty slot', () => {
    expect(minimapKindOf(node('addsat'))).toBe('hidden');
  });
});
