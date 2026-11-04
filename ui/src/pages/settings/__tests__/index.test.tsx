// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import SettingsPage from '..';

const licenseState = vi.hoisted(() => ({ loading: false }));

vi.mock('@/hooks/useLicense', () => ({
  useLicenseState: () => licenseState,
}));
vi.mock('@/pages/license', () => ({
  default: ({ children }: { children?: React.ReactNode }) => (
    <>
      <h2>Plan & features</h2>
      {children}
    </>
  ),
}));
vi.mock('@/pages/remote-nodes', () => ({
  default: () => <h2 id="remote-nodes">Remote Nodes</h2>,
}));

const scrollIntoView = vi.fn();

beforeEach(() => {
  licenseState.loading = false;
  Element.prototype.scrollIntoView = scrollIntoView;
});

afterEach(() => {
  scrollIntoView.mockReset();
});

function renderPage(path = '/settings') {
  const setTitle = vi.fn();
  const view = render(
    <MemoryRouter initialEntries={[path]}>
      <AppBarContext.Provider value={{ setTitle } as never}>
        <SettingsPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
  return { ...view, setTitle };
}

describe('SettingsPage', () => {
  it('shows the plan and remote nodes on one page', () => {
    const { setTitle } = renderPage();

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      'Settings'
    );
    expect(
      screen.getByRole('heading', { name: 'Plan & features' })
    ).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Remote Nodes' })).toBeVisible();
    expect(setTitle).toHaveBeenCalledWith('Settings');
  });

  // The plan section changes height when the license status arrives, so the
  // page scrolls to remote nodes only after that.
  it('scrolls to remote nodes once the license status is known', () => {
    licenseState.loading = true;
    const { rerender } = renderPage('/settings#remote-nodes');
    expect(scrollIntoView).not.toHaveBeenCalled();

    licenseState.loading = false;
    rerender(
      <MemoryRouter initialEntries={['/settings#remote-nodes']}>
        <AppBarContext.Provider value={{ setTitle: vi.fn() } as never}>
          <SettingsPage />
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(scrollIntoView).toHaveBeenCalledTimes(1);
    expect(scrollIntoView.mock.contexts[0]).toBe(
      document.getElementById('remote-nodes')
    );
  });
});
