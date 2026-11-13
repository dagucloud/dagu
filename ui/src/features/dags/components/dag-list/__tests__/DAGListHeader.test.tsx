// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import DAGListHeader from '../DAGListHeader';

vi.mock('../../common', () => ({
  CreateDAGButton: () => null,
}));

describe('DAGListHeader', () => {
  it('labels the definitions list as Workflows', () => {
    render(
      <MemoryRouter>
        <DAGListHeader />
      </MemoryRouter>
    );

    expect(screen.getByRole('heading', { name: /^workflows$/i })).toBeVisible();
    expect(
      screen.queryByRole('heading', { name: /dag definitions/i })
    ).toBeNull();
  });

  it('shows the number of workflows next to the title', () => {
    render(
      <MemoryRouter>
        <DAGListHeader resultCount={1234} />
      </MemoryRouter>
    );

    expect(screen.getByTestId('workflow-count')).toHaveTextContent(
      /1,?234 workflows/
    );
  });

  it('shows the number of runs in the runs mode', () => {
    render(
      <MemoryRouter>
        <DAGListHeader mode="runs" resultCount={1} />
      </MemoryRouter>
    );

    expect(screen.getByTestId('workflow-count')).toHaveTextContent(/^1 run$/);
  });

  it('marks the number of runs with a plus when more are available', () => {
    render(
      <MemoryRouter>
        <DAGListHeader mode="runs" resultCount={100} hasMore />
      </MemoryRouter>
    );

    expect(screen.getByTestId('workflow-count')).toHaveTextContent(
      /^100\+ runs$/
    );
  });

  it('keeps the plural for one run when more are available', () => {
    render(
      <MemoryRouter>
        <DAGListHeader mode="runs" resultCount={1} hasMore />
      </MemoryRouter>
    );

    expect(screen.getByTestId('workflow-count')).toHaveTextContent(
      /^1\+ runs$/
    );
  });

  it('puts the mode toggle after the title and before the count', () => {
    render(
      <MemoryRouter>
        <DAGListHeader mode="runs" resultCount={3} />
      </MemoryRouter>
    );

    const title = screen.getByRole('heading', { name: /^workflows$/i });
    const toggle = screen.getByRole('group', { name: 'Show' });
    const count = screen.getByTestId('workflow-count');
    const follows = (a: Node, b: Node) =>
      Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
    expect(follows(title, toggle)).toBe(true);
    expect(follows(toggle, count)).toBe(true);
  });
});
