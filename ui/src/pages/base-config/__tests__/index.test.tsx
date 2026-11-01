// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import BaseConfigPage from '..';

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: vi.fn(), PUT: vi.fn() }),
  useQuery: vi.fn(),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => true,
}));

vi.mock('../../../contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { writeDags: true } }),
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));

vi.mock('@/features/dags/components/dag-editor/DAGEditorWithDocs', () => ({
  default: ({ value }: { value: string }) => (
    <div data-testid="base-config-editor">{value}</div>
  ),
}));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;

function renderPage() {
  render(
    <AppBarContext.Provider
      value={
        {
          setTitle: vi.fn(),
          selectedRemoteNode: 'local',
          workspaceSelection: { kind: 'all' },
        } as never
      }
    >
      <BaseConfigPage />
    </AppBarContext.Provider>
  );
}

describe('BaseConfigPage', () => {
  beforeEach(() => {
    useQueryMock.mockReset();
  });

  // An editable editor before the config arrives could save blank content
  // over the real config.
  it('keeps the editor closed until the config loads', () => {
    useQueryMock.mockReturnValue({
      data: undefined,
      error: undefined,
      mutate: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(screen.queryByTestId('base-config-editor')).toBeNull();
  });

  it('opens the editor with the loaded config', () => {
    useQueryMock.mockReturnValue({
      data: { spec: 'env:\n  - TZ: UTC\n' },
      error: undefined,
      mutate: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('base-config-editor')).toHaveTextContent(
      'TZ: UTC'
    );
  });

  it('reports a failed load instead of an empty editor', () => {
    useQueryMock.mockReturnValue({
      data: undefined,
      error: { code: 'internal_error', message: 'permission denied' },
      mutate: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByRole('alert')).toHaveTextContent('permission denied');
    expect(screen.queryByTestId('base-config-editor')).toBeNull();
  });
});
