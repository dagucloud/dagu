// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import DAGPagination from '../DAGPagination';

describe('DAGPagination', () => {
  it('offers page sizes as buttons that mark the current size', () => {
    const onPageLimitChange = vi.fn();
    render(
      <DAGPagination
        totalPages={3}
        page={1}
        pageLimit={25}
        pageChange={vi.fn()}
        onPageLimitChange={onPageLimitChange}
      />
    );

    expect(screen.getByRole('button', { name: '25' })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    fireEvent.click(screen.getByRole('button', { name: '50' }));

    expect(onPageLimitChange).toHaveBeenCalledWith(50);
  });
});
