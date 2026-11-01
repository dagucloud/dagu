// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, NodeStatusLabel } from '@/api/v1/schema';
import NodeStatusChip from '../NodeStatusChip';

function preferReducedMotion(matches: boolean) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  });
}

// The spinner frame before and after `ms` of the clock.
function spinnerFrames(ms: number): [string, string] {
  const { container } = render(
    <NodeStatusChip status={NodeStatus.Retrying}>
      {NodeStatusLabel.retrying}
    </NodeStatusChip>
  );
  const spinner = container.querySelector('[aria-hidden="true"]')!;
  const before = spinner.textContent ?? '';
  act(() => {
    vi.advanceTimersByTime(ms);
  });
  return [before, spinner.textContent ?? ''];
}

describe('NodeStatusChip spinner', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('steps through its frames', () => {
    preferReducedMotion(false);
    const [before, after] = spinnerFrames(80);
    expect(after).not.toBe(before);
  });

  it('stays still when the user asks for reduced motion', () => {
    preferReducedMotion(true);
    const [before, after] = spinnerFrames(80);
    expect(after).toBe(before);
  });
});
