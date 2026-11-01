// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
} from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  MonitoringOffer,
  useMonitoringOfferState,
} from '@/components/MonitoringOffer';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useIsAdmin } from '@/contexts/AuthContext';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import { LicenseContext } from '@/contexts/LicenseContext';

vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: vi.fn() }));

const useIsAdminMock = vi.mocked(useIsAdmin);

const community: LicenseStatus = {
  valid: false,
  plan: '',
  expiry: '',
  features: [],
  gracePeriod: false,
  graceEndsAt: '',
  community: true,
  source: '',
  warningCode: '',
  error: '',
};

const heading = 'Get an email if this server stops';

function renderOffer(
  state: { license?: LicenseStatus; error?: unknown } = { license: community },
  remoteNode = 'local'
) {
  return render(
    <MemoryRouter>
      <AppBarContext.Provider
        value={{ selectedRemoteNode: remoteNode } as never}
      >
        <LicenseContext.Provider value={{ ...state, mutate: vi.fn() }}>
          <MonitoringOffer />
        </LicenseContext.Provider>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

// parity:overview-monitoring-offer parity:shell-monitoring-offer
describe('MonitoringOffer', () => {
  beforeEach(() => {
    localStorage.clear();
    useIsAdminMock.mockReturnValue(true);
  });

  it('offers a Community admin free monitoring', () => {
    renderOffer();

    expect(screen.getByRole('region', { name: heading })).toBeVisible();
    expect(
      screen.getByRole('link', { name: 'Set up for free' })
    ).toHaveAttribute('href', '/license#monitoring');
  });

  it.each([
    ['a non-admin', false, { license: community }],
    [
      'a licensed server',
      true,
      {
        license: { ...community, community: false, valid: true, plan: 'team' },
      },
    ],
    ['a license still loading', true, {}],
    ['an unreadable license', true, { error: new Error('unavailable') }],
    [
      'an environment license',
      true,
      { license: { ...community, source: 'env' } },
    ],
  ])('stays hidden for %s', (_, admin, state) => {
    useIsAdminMock.mockReturnValue(admin);

    renderOffer(state);

    expect(screen.queryByText(heading)).not.toBeInTheDocument();
  });

  it('stays dismissed for that server only', () => {
    renderOffer();
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText(heading)).not.toBeInTheDocument();
    cleanup();

    renderOffer();
    expect(screen.queryByText(heading)).not.toBeInTheDocument();
    cleanup();

    renderOffer(undefined, 'edge');
    expect(screen.getByText(heading)).toBeVisible();
  });
});

// The workspace shows the offer as a notice through this hook.
// parity:overview-monitoring-offer parity:shell-monitoring-offer
describe('useMonitoringOfferState', () => {
  beforeEach(() => {
    localStorage.clear();
    useIsAdminMock.mockReturnValue(true);
  });

  function renderState(
    state: { license?: LicenseStatus; error?: unknown } = {
      license: community,
    },
    remoteNode = 'local'
  ) {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <AppBarContext.Provider
        value={{ selectedRemoteNode: remoteNode } as never}
      >
        <LicenseContext.Provider value={{ ...state, mutate: vi.fn() }}>
          {children}
        </LicenseContext.Provider>
      </AppBarContext.Provider>
    );
    return renderHook(() => useMonitoringOfferState(), { wrapper });
  }

  it('is visible for a Community admin', () => {
    expect(renderState().result.current.visible).toBe(true);
  });

  it.each([
    ['a non-admin', false, { license: community }],
    [
      'a licensed server',
      true,
      {
        license: { ...community, community: false, valid: true, plan: 'team' },
      },
    ],
    ['a license still loading', true, {}],
    ['an unreadable license', true, { error: new Error('unavailable') }],
    [
      'an environment license',
      true,
      { license: { ...community, source: 'env' } },
    ],
  ])('is hidden for %s', (_, admin, state) => {
    useIsAdminMock.mockReturnValue(admin);
    expect(renderState(state).result.current.visible).toBe(false);
  });

  it('dismisses under the key of the selected server', () => {
    const { result } = renderState(undefined, 'edge');
    act(() => result.current.dismiss());
    expect(result.current.visible).toBe(false);
    expect(localStorage.getItem('dagu.monitoringOffer:edge')).toBe('dismissed');
    expect(renderState().result.current.visible).toBe(true);
  });

  it('hides the card after a dismissal through the hook', () => {
    const { result } = renderState();
    act(() => result.current.dismiss());
    renderOffer();
    expect(screen.queryByText(heading)).not.toBeInTheDocument();
  });
});
