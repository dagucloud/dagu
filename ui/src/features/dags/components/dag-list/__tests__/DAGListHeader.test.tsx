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
});
