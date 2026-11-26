// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import type React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { DAGRecentRuns } from '../DAGRecentRuns';

const h = vi.hoisted(() => ({
  reads: [] as unknown[],
  mutate: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (_path: string, init: unknown) => {
    h.reads.push(init);
    return { data: { dagRuns: [] }, isLoading: false, mutate: h.mutate };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/features/dag-runs/lib/useOpenPage', () => ({
  useOpenPage: () => vi.fn(),
}));

function Wrapper({ children }: { children: React.ReactNode }) {
  return (
    <UserPreferencesProvider>
      <I18nProvider>{children}</I18nProvider>
    </UserPreferencesProvider>
  );
}

function activity() {
  return { runs: [], truncated: false };
}

describe('DAGRecentRuns', () => {
  beforeEach(() => {
    h.reads = [];
    h.mutate.mockClear();
  });

  // The panel reads one page of the latest runs. It does not poll: a new
  // activity, which the change feed of the list drives, reads it again.
  it('reads the latest runs again only when the list reads a new activity', () => {
    const first = activity();
    const view = render(
      <DAGRecentRuns fileName="alpha.yaml" activity={first} />,
      { wrapper: Wrapper }
    );
    expect(h.reads[0]).toEqual({
      params: {
        query: { remoteNode: 'local', limit: 10 },
        path: { fileName: 'alpha.yaml' },
      },
    });

    view.rerender(<DAGRecentRuns fileName="alpha.yaml" activity={first} />);
    expect(h.mutate).not.toHaveBeenCalled();

    view.rerender(
      <DAGRecentRuns fileName="alpha.yaml" activity={activity()} />
    );
    expect(h.mutate).toHaveBeenCalledTimes(1);
  });
});
