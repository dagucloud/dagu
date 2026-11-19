// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { LegacyToolboxRedirect } from '../LegacyToolboxRedirect';

function Where() {
  const location = useLocation();
  return (
    <div data-testid="where">
      {location.pathname + location.search + location.hash}
    </div>
  );
}

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/toolbox/*" element={<LegacyToolboxRedirect />} />
        <Route path="/ai/*" element={<Where />} />
      </Routes>
    </MemoryRouter>
  );
}

describe('LegacyToolboxRedirect', () => {
  it('sends /toolbox to /ai', () => {
    renderAt('/toolbox');
    expect(screen.getByTestId('where')).toHaveTextContent('/ai');
  });

  it('keeps the rest of the path, the query and the hash', () => {
    renderAt('/toolbox/skills/triage?remoteNode=a#x');
    expect(screen.getByTestId('where')).toHaveTextContent(
      '/ai/skills/triage?remoteNode=a#x'
    );
  });
});
