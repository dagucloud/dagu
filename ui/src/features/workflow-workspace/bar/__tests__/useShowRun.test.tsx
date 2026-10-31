// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { useShowRun } from '../useShowRun';

function Where() {
  const location = useLocation();
  return (
    <output data-testid="where">{location.pathname + location.search}</output>
  );
}

function wrapperAt(url: string) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <MemoryRouter initialEntries={[url]}>
        <Where />
        <Routes>
          <Route path="/dags/:fileName" element={children} />
        </Routes>
      </MemoryRouter>
    );
  };
}

describe('useShowRun', () => {
  // A run that starts from the bar stays on the step: "?run=<old>&step=merge"
  // becomes "?run=<new>&step=merge", and the node view stays too.
  it('selects the run and keeps the step and the node view', () => {
    const { result } = renderHook(() => useShowRun(), {
      wrapper: wrapperAt('/dags/etl?run=old&step=merge&ndv=expanded'),
    });
    act(() => result.current('new'));
    const where = document.querySelector('[data-testid="where"]');
    expect(where).toHaveTextContent(
      '/dags/etl?run=new&step=merge&ndv=expanded'
    );
  });

  it('writes only ?run when no step is selected', () => {
    const { result } = renderHook(() => useShowRun(), {
      wrapper: wrapperAt('/dags/etl'),
    });
    act(() => result.current('new'));
    expect(document.querySelector('[data-testid="where"]')).toHaveTextContent(
      '/dags/etl?run=new'
    );
  });
});
