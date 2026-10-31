// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  RuntimeProfileStatus,
  WebhookAuthMode,
  type components,
} from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import DAGActions from '../../components/common/DAGActions';
import DAGSettingsTab from '../../components/dag-details/DAGSettingsTab';
import WebhookProfileSelectionCard from '../../components/dag-details/WebhookProfileSelectionCard';
import { useProfiles } from '../useProfiles';

const h = vi.hoisted(() => ({ GET: vi.fn() }));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return {
    useQuery: createQueryHook({ GET: h.GET } as never, '/'),
    useClient: () => ({ GET: h.GET, PUT: vi.fn(), POST: vi.fn() }),
  };
});
vi.mock('@/contexts/AuthContext', () => ({
  useCanManageProfiles: () => true,
  useIsAdmin: () => true,
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: true } }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));
vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));
// The dialog shows the profiles that DAGActions gives it.
vi.mock('../../components/dag-execution/StartDAGModal', () => ({
  default: ({
    visible,
    profiles,
  }: {
    visible: boolean;
    profiles?: Array<{ name: string }>;
  }) =>
    visible ? (
      <div role="dialog" aria-label="Start dialog">
        {(profiles ?? []).map((p) => (
          <span key={p.name}>start:{p.name}</span>
        ))}
      </div>
    ) : null,
}));

type RuntimeProfile = components['schemas']['RuntimeProfileResponse'];
type WebhookDetails = components['schemas']['WebhookDetails'];

function profile(name: string, status: RuntimeProfileStatus): RuntimeProfile {
  return {
    id: `id-${name}`,
    name,
    status,
    protected: false,
    entries: [],
    createdAt: '2026-08-07T00:00:00Z',
    updatedAt: '2026-08-07T00:00:00Z',
  } as RuntimeProfile;
}

const PROFILES = [
  profile('prod', RuntimeProfileStatus.active),
  profile('old', RuntimeProfileStatus.disabled),
];

const webhook: WebhookDetails = {
  id: 'webhook-1',
  dagName: 'example',
  tokenPrefix: 'dagu_wh_',
  enabled: true,
  authMode: WebhookAuthMode.token_only,
  hmac: { enabled: false, secretConfigured: false },
  profileSelection: { allowedProfiles: [] },
  profileTokens: [],
  createdAt: '2026-08-07T00:00:00Z',
  updatedAt: '2026-08-07T00:00:00Z',
};

function profileCalls() {
  return h.GET.mock.calls.filter((call) => call[0] === '/profiles');
}

function Fresh({ children }: { children: ReactNode }) {
  return (
    <SWRConfig value={{ provider: () => new Map() }}>{children}</SWRConfig>
  );
}

beforeEach(() => {
  h.GET.mockReset();
  h.GET.mockImplementation(async (path: string) => {
    if (path === '/profiles') return { data: { profiles: PROFILES } };
    if (path === '/dags/{fileName}/settings') return { data: { profile: '' } };
    if (path === '/dags/{fileName}') {
      return { data: { dag: { name: 'example' } } };
    }
    return { error: { message: `unexpected ${path}` } };
  });
});

// parity:dup-18 parity:start-modal-profile
describe('useProfiles shared key', () => {
  it('sends one /profiles request for the Start dialog, Runtime profile and Webhook', async () => {
    const user = userEvent.setup();
    render(
      <Fresh>
        <MemoryRouter>
          <DAGActions
            fileName="example.yaml"
            dag={{ name: 'example' }}
            displayMode="full"
            only={['start']}
          />
          <DAGSettingsTab fileName="example.yaml" />
          <WebhookProfileSelectionCard
            fileName="example.yaml"
            isAdmin
            remoteNode="local"
            webhook={webhook}
            onActiveProfileNamesChange={vi.fn()}
            onWebhookChange={vi.fn()}
          />
        </MemoryRouter>
      </Fresh>
    );

    await user.click(screen.getByRole('button', { name: 'Start' }));
    expect(await screen.findByText('start:prod')).toBeVisible();
    expect(screen.getByText('start:old')).toBeVisible();
    expect(await screen.findByRole('checkbox', { name: 'prod' })).toBeVisible();
    expect(screen.getByText('Default profile')).toBeVisible();

    expect(profileCalls()).toEqual([
      ['/profiles', { params: { query: { remoteNode: 'local' } } }],
    ]);
  });
});

describe('useProfiles', () => {
  it('sends nothing while disabled', async () => {
    const { result } = renderHook(() => useProfiles({ enabled: false }), {
      wrapper: Fresh,
    });
    await act(async () => {});
    expect(result.current.profiles).toEqual([]);
    expect(result.current.isLoading).toBe(false);
    expect(profileCalls()).toEqual([]);
  });

  it('keeps only the active profiles in activeProfiles', async () => {
    const { result } = renderHook(() => useProfiles({ enabled: true }), {
      wrapper: Fresh,
    });
    await waitFor(() => expect(result.current.profiles).toHaveLength(2));
    expect(result.current.activeProfiles.map((p) => p.name)).toEqual(['prod']);
    expect(result.current.error).toBeUndefined();
  });

  it('reads the remote node of the context, or the override', async () => {
    function Remote({ children }: { children: ReactNode }) {
      return (
        <Fresh>
          <RemoteNodeProvider remoteNode="edge">{children}</RemoteNodeProvider>
        </Fresh>
      );
    }
    const { result } = renderHook(
      () =>
        [
          useProfiles({ enabled: true }),
          useProfiles({ enabled: true, remoteNode: 'worker-a' }),
        ] as const,
      { wrapper: Remote }
    );
    await waitFor(() => expect(result.current[1].profiles).toHaveLength(2));
    expect(profileCalls()).toEqual([
      ['/profiles', { params: { query: { remoteNode: 'edge' } } }],
      ['/profiles', { params: { query: { remoteNode: 'worker-a' } } }],
    ]);
  });

  it('gives the error and sends the request again on retry', async () => {
    h.GET.mockResolvedValueOnce({ error: { message: 'Down' } });
    const { result } = renderHook(() => useProfiles({ enabled: true }), {
      wrapper: Fresh,
    });
    await waitFor(() =>
      expect(result.current.error).toEqual({ message: 'Down' })
    );
    act(() => result.current.retry());
    await waitFor(() => expect(result.current.profiles).toHaveLength(2));
    expect(result.current.error).toBeUndefined();
    expect(profileCalls()).toHaveLength(2);
  });
});
