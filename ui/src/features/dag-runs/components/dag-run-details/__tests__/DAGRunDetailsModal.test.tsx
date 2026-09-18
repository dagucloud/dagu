// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useBoundedDAGRunDetails } from '../../../hooks/useBoundedDAGRunDetails';
import DAGRunDetailsModal from '../DAGRunDetailsModal';

vi.mock('../../../hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(),
}));

vi.mock('../DAGRunDetailsContent', () => ({
  default: ({
    initialTab,
    fillHeight,
  }: {
    initialTab: string;
    fillHeight?: boolean;
  }) => (
    <div
      data-fill-height={String(fillHeight)}
      data-initial-tab={initialTab}
      data-testid="dag-run-content"
    />
  ),
}));

afterEach(() => {
  vi.clearAllMocks();
});

describe('DAGRunDetailsModal', () => {
  it('navigates histories and prevents scrolling', () => {
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: { dagRunId: 'run-1', name: 'example' },
      isLoading: false,
      isValidating: false,
      refresh: vi.fn(),
    } as unknown as ReturnType<typeof useBoundedDAGRunDetails>);
    const onNavigate = vi.fn();
    render(
      <MemoryRouter>
        <DAGRunDetailsModal
          name="example"
          dagRunId="run-1"
          isOpen
          onClose={vi.fn()}
          onNavigate={onNavigate}
        />
      </MemoryRouter>
    );
    for (const [key, direction] of [
      ['ArrowDown', 'down'],
      ['ArrowUp', 'up'],
    ]) {
      const event = new KeyboardEvent('keydown', {
        key,
        bubbles: true,
        cancelable: true,
      });
      fireEvent(window, event);
      expect(onNavigate).toHaveBeenLastCalledWith(direction);
      expect(event.defaultPrevented).toBe(true);
    }
    const input = document.createElement('input');
    document.body.appendChild(input);
    input.focus();
    const event = new KeyboardEvent('keydown', {
      key: 'ArrowDown',
      bubbles: true,
      cancelable: true,
    });
    fireEvent(input, event);
    expect(event.defaultPrevented).toBe(false);
    expect(onNavigate).toHaveBeenCalledTimes(2);
    input.remove();
  });

  it('fills the modal content height when artifacts are opened from the status tab', () => {
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: { dagRunId: 'run-1', name: 'example' },
      error: undefined,
      isLoading: false,
      isValidating: false,
      refresh: vi.fn(),
    } as unknown as ReturnType<typeof useBoundedDAGRunDetails>);

    render(
      <MemoryRouter>
        <DAGRunDetailsModal
          name="example"
          dagRunId="run-1"
          isOpen
          onClose={() => {}}
        />
      </MemoryRouter>
    );

    expect(screen.getByTestId('dag-run-content')).toHaveAttribute(
      'data-initial-tab',
      'status'
    );
    expect(screen.getByTestId('dag-run-content')).toHaveAttribute(
      'data-fill-height',
      'true'
    );
  });
});
