// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ParallelExecutionModal } from '../ParallelExecutionModal';

vi.mock('@/hooks/api', () => ({
  useQuery: () => ({ data: undefined, isLoading: false, mutate: vi.fn() }),
}));

const LONG = 'VOLUME_SNAPSHOT_NAME="snapshot-' + 'a'.repeat(120) + '"';

const subRuns = [
  { dagRunId: 'w-1', dagName: 'worker', params: 'N=1' },
  { dagRunId: 'w-2', dagName: 'worker', params: LONG },
];

function renderModal() {
  return render(
    <ParallelExecutionModal
      isOpen
      onClose={vi.fn()}
      stepName="fan"
      subDAGName="worker"
      subRuns={subRuns}
      onSelectSubRun={vi.fn()}
      rootDagName="example"
      rootDagRunId="run-1"
    />
  );
}

function paramsCell(text: string): HTMLElement {
  const cell = screen.getByText(text).closest('[data-params-scroll]');
  if (!(cell instanceof HTMLElement)) throw new Error('params cell missing');
  return cell;
}

// jsdom has no Element.scrollBy; the tests only check that the key handler
// scrolls the right cell in the right direction.
const scrollBy = vi.fn();
const hadScrollBy = 'scrollBy' in Element.prototype;
const originalScrollBy = Element.prototype.scrollBy;

beforeEach(() => {
  scrollBy.mockClear();
  Element.prototype.scrollBy = scrollBy;
});

afterEach(() => {
  if (hadScrollBy) {
    Element.prototype.scrollBy = originalScrollBy;
  } else {
    delete (Element.prototype as { scrollBy?: unknown }).scrollBy;
  }
});

describe('ParallelExecutionModal', () => {
  it('scrolls the selected params with the left and right arrow keys', () => {
    renderModal();

    fireEvent.keyDown(window, { key: 'ArrowDown' });
    fireEvent.keyDown(window, { key: 'ArrowDown' });
    fireEvent.keyDown(window, { key: 'ArrowRight' });

    expect(scrollBy).toHaveBeenCalledTimes(1);
    expect(scrollBy.mock.instances[0]).toBe(paramsCell(LONG));
    expect(scrollBy).toHaveBeenLastCalledWith(
      expect.objectContaining({ left: expect.any(Number) })
    );
    const right = (scrollBy.mock.calls[0]?.[0] as ScrollToOptions).left ?? 0;
    expect(right).toBeGreaterThan(0);

    fireEvent.keyDown(window, { key: 'ArrowLeft' });

    expect(scrollBy).toHaveBeenCalledTimes(2);
    expect(scrollBy.mock.instances[1]).toBe(paramsCell(LONG));
    const left = (scrollBy.mock.calls[1]?.[0] as ScrollToOptions).left ?? 0;
    expect(left).toBe(-right);
  });

  it('ignores the horizontal arrow keys until a row is selected', () => {
    renderModal();

    fireEvent.keyDown(window, { key: 'ArrowRight' });
    fireEvent.keyDown(window, { key: 'ArrowLeft' });

    expect(scrollBy).not.toHaveBeenCalled();
  });
});
