// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AIEndpointAuth, AIEndpointProtocol } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import type { AIEndpoint } from '@/features/ai-endpoints/endpoint';
import { ModelsDialog } from '../ModelsDialog';

const mocks = vi.hoisted(() => ({
  data: undefined as unknown,
  init: undefined as unknown,
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (_path: string, init: unknown) => {
    mocks.init = init;
    return { data: mocks.data, error: undefined };
  },
}));

const endpoint: AIEndpoint = {
  name: 'cle1',
  preset: 'anthropic',
  protocol: AIEndpointProtocol.anthropic,
  baseUrl: 'https://api.anthropic.com',
  auth: AIEndpointAuth.x_api_key,
  scope: 'workspace:team',
  allow: { dags: ['*'] },
  createdAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-11-01T00:00:00Z',
};

function renderDialog(wrap: (dialog: ReactNode) => ReactNode = (d) => d) {
  render(wrap(<ModelsDialog endpoint={endpoint} onClose={() => {}} />));
}

beforeEach(() => {
  mocks.data = {
    models: ['claude-opus-5-5', 'claude-haiku-5', 'claude-sonnet-5'],
    count: 3,
  };
  mocks.init = undefined;
});

describe('ModelsDialog', () => {
  it('asks the remote node of its scope, not the one of the app bar', () => {
    renderDialog((dialog) => (
      <AppBarContext.Provider value={{ selectedRemoteNode: 'node-a' } as never}>
        <RemoteNodeProvider remoteNode="node-b">{dialog}</RemoteNodeProvider>
      </AppBarContext.Provider>
    ));
    expect(mocks.init).toEqual({
      params: {
        path: { name: 'cle1' },
        query: { remoteNode: 'node-b', workspace: 'team' },
      },
    });
  });

  it('filters the names with the search box and counts them', () => {
    renderDialog();
    expect(screen.getByRole('status')).toHaveTextContent('3 of 3 models');
    fireEvent.change(screen.getByLabelText('Search the models'), {
      target: { value: 'HAIKU' },
    });
    expect(screen.getByRole('status')).toHaveTextContent('1 of 3 models');
    expect(screen.getByRole('list', { name: 'Models' })).toHaveTextContent(
      'claude-haiku-5'
    );
  });
});
