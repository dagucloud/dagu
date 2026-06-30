// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useInfinite } from '@/hooks/api';
import { SearchStateProvider } from '@/contexts/SearchStateContext';
import SearchPage from '../index';

vi.mock('@/hooks/api', () => ({
  useInfinite: vi.fn(),
}));

vi.mock('@/features/search/components/SearchResult', () => ({
  __esModule: true,
  default: ({ results }: { results: unknown[] }) => (
    <div data-testid="dag-results">{results.length}</div>
  ),
}));

class IntersectionObserverMock {
  observe() {
    return;
  }

  disconnect() {
    return;
  }

  unobserve() {
    return;
  }
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  Object.defineProperty(window, 'IntersectionObserver', {
    writable: true,
    value: IntersectionObserverMock,
  });
  window.sessionStorage.clear();
});

const mockUseInfinite = useInfinite as unknown as {
  mockReturnValue(value: unknown): void;
};

function renderSearchPage(initialEntry: string) {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <SearchStateProvider>
        <SearchPage />
      </SearchStateProvider>
    </MemoryRouter>
  );
}

describe('SearchPage', () => {
  it('keeps existing results visible when loading more fails and allows retry', async () => {
    const mutate = vi.fn();

    mockUseInfinite.mockReturnValue({
      data: [
        {
          results: [
            {
              fileName: 'build',
              name: 'build',
              hasMoreMatches: false,
              matches: [],
            },
          ],
          hasMore: true,
          nextCursor: 'cursor-1',
        },
      ],
      error: { message: 'load failed' },
      isLoading: false,
      isValidating: false,
      setSize: vi.fn(),
      mutate,
    } as never);

    renderSearchPage('/search?q=needle');

    expect(screen.getByTestId('dag-results')).toHaveTextContent('1');
    expect(screen.getByText('load failed')).toBeInTheDocument();

    await userEvent.click(
      screen.getByRole('button', { name: 'Retry load more' })
    );

    expect(mutate).toHaveBeenCalled();
  });

  it('allows clearing a previously submitted search', async () => {
    mockUseInfinite.mockReturnValue({
      data: [],
      error: undefined,
      isLoading: false,
      isValidating: false,
      setSize: vi.fn(),
      mutate: vi.fn(),
    } as never);

    renderSearchPage('/search?q=needle');

    const input = screen.getByRole('searchbox');
    await userEvent.clear(input);
    await userEvent.click(screen.getByRole('button', { name: 'Search' }));

    expect(
      screen.getByText('Enter a search term and press Enter or click Search')
    ).toBeInTheDocument();
  });

});
