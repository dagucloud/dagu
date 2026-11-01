// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { components } from '@/api/v1/schema';
import { continuousNodes, pickContinuousNode } from '../continuousNode';

type Node = components['schemas']['Node'];

const n = (name: string, continuous?: boolean) =>
  ({ step: { name }, continuous }) as unknown as Node;

describe('pickContinuousNode', () => {
  const details = { nodes: [n('a'), n('b', true), n('c', true)] };

  it('takes the selected step when it is continuous', () => {
    expect(pickContinuousNode(details, 'c')?.step.name).toBe('c');
  });

  it('takes the first continuous step otherwise', () => {
    expect(pickContinuousNode(details, 'a')?.step.name).toBe('b');
    expect(pickContinuousNode(details)?.step.name).toBe('b');
    expect(pickContinuousNode(details, null)?.step.name).toBe('b');
  });

  it('has none for a run without a continuous step', () => {
    expect(pickContinuousNode({ nodes: [n('a')] }, 'a')).toBeNull();
    expect(continuousNodes(undefined)).toEqual([]);
  });
});
