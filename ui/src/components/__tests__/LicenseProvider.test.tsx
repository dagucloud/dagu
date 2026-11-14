// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useContext, useState } from 'react';
import { SWRConfig, useSWRConfig } from 'swr';
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { LicenseProvider } from '../LicenseProvider';
import { LicenseContext } from '@/contexts/LicenseContext';
import { useLicenseState } from '@/hooks/useLicense';
import type { LicenseStatus } from '@/contexts/ConfigContext';

vi.hoisted(() =>
  vi.stubGlobal('getConfig', () => ({ apiURL: 'http://localhost/api/v1' }))
);
const paid = {
  valid: true,
  plan: 'pro',
  expiry: '',
  features: ['audit'],
  gracePeriod: false,
  graceEndsAt: '',
  community: false,
  source: 'file',
  warningCode: '',
  error: '',
} satisfies LicenseStatus;
const community = {
  ...paid,
  plan: '',
  valid: false,
  community: true,
  features: [],
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
function Probe() {
  const { license, loading, error } = useLicenseState();
  const { mutate } = useContext(LicenseContext)!;
  const { mutate: clearCache } = useSWRConfig();
  return (
    <>
      <output aria-label="Status">
        {loading
          ? 'loading'
          : error
            ? 'unavailable'
            : license.community
              ? 'community'
              : license.plan}
      </output>
      <button
        onClick={() =>
          void mutate({ ...paid, plan: 'team' }, { revalidate: false })
        }
      >
        Activate Team
      </button>
      <button onClick={() => void mutate(community, { revalidate: false })}>
        Deactivate
      </button>
      <button
        onClick={() =>
          void clearCache(() => true, undefined, { revalidate: false })
        }
      >
        Clear session
      </button>
      <button onClick={() => void mutate()}>Refresh</button>
    </>
  );
}
function Harness({ initialNode = 'local' }: { initialNode?: string }) {
  const [node, setNode] = useState(initialNode);
  return (
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <button onClick={() => setNode('remote')}>Remote</button>
      <button onClick={() => setNode('local')}>Local</button>
      <LicenseProvider enabled remoteNode={node} initialLicense={paid}>
        <Probe />
      </LicenseProvider>
    </SWRConfig>
  );
}
beforeEach(() => localStorage.clear());
afterEach(() => vi.unstubAllGlobals());

describe('node-specific license status', () => {
  it('does not restore bootstrap entitlements after a session reset', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(Response.json(paid))
        .mockResolvedValue(
          Response.json({ message: 'offline' }, { status: 503 })
        )
    );
    render(<Harness />);
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    await userEvent.click(screen.getByRole('button', { name: 'Deactivate' }));
    expect(screen.getByLabelText('Status')).toHaveTextContent('community');
    await userEvent.click(
      screen.getByRole('button', { name: 'Clear session' })
    );
    expect(screen.getByLabelText('Status')).toHaveTextContent('loading');
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() =>
      expect(screen.getByLabelText('Status')).toHaveTextContent('unavailable')
    );
  });

  it('keeps pending and late remote responses separate from local status', async () => {
    const remote = deferred<Response>();
    vi.stubGlobal(
      'fetch',
      vi.fn((request: Request) =>
        new URL(request.url).searchParams.get('remoteNode') === 'remote'
          ? remote.promise.then((response) => response.clone())
          : Promise.resolve(Response.json(paid))
      )
    );
    render(<Harness />);
    await waitFor(() =>
      expect(screen.getByLabelText('Status')).toHaveTextContent('pro')
    );
    await userEvent.click(screen.getByRole('button', { name: 'Remote' }));
    expect(screen.getByLabelText('Status')).toHaveTextContent('loading');
    await userEvent.click(screen.getByRole('button', { name: 'Local' }));
    expect(screen.getByLabelText('Status')).toHaveTextContent('pro');
    await act(async () => {
      remote.resolve(Response.json(community));
      await remote.promise;
    });
    expect(screen.getByLabelText('Status')).toHaveTextContent('pro');
    await userEvent.click(screen.getByRole('button', { name: 'Remote' }));
    await waitFor(() =>
      expect(screen.getByLabelText('Status')).toHaveTextContent('community')
    );
  });

  it('never seeds a persisted remote selection with the local license', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          Response.json({ message: 'offline' }, { status: 503 })
        )
    );
    render(<Harness initialNode="remote" />);
    expect(screen.getByLabelText('Status')).toHaveTextContent('loading');
    await waitFor(() =>
      expect(screen.getByLabelText('Status')).toHaveTextContent('unavailable')
    );
  });

  it('does not let an earlier status request overwrite an activation', async () => {
    const response = deferred<Response>();
    vi.stubGlobal(
      'fetch',
      vi.fn(() => response.promise)
    );
    render(<Harness />);
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    await userEvent.click(
      screen.getByRole('button', { name: 'Activate Team' })
    );
    expect(screen.getByLabelText('Status')).toHaveTextContent('team');
    await act(async () => {
      response.resolve(Response.json(community));
      await response.promise;
    });
    expect(screen.getByLabelText('Status')).toHaveTextContent('team');
  });
});
