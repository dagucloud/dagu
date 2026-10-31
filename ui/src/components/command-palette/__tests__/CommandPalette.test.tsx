// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { useQuery } from '@/hooks/api';
import { I18nProvider } from '@/i18n/I18nProvider';
import { WorkspaceKind } from '@/lib/workspace';
import { CommandPaletteProvider } from '../CommandPaletteProvider';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));

vi.mock('@/hooks/useNavigationSections', () => ({
  useNavigationSections: () => ({
    mainItems: [{ to: '/dag-runs', text: 'Executions', icon: null }],
    sections: [
      {
        key: 'monitor',
        label: 'Monitor',
        items: [{ to: '/audit-logs', text: 'Audit Logs', icon: null }],
      },
    ],
  }),
}));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;

function CurrentLocation() {
  const location = useLocation();
  return (
    <output aria-label="location">{location.pathname + location.search}</output>
  );
}

function renderPalette() {
  render(
    <UserPreferencesProvider>
      <I18nProvider>
        <MemoryRouter initialEntries={['/dags']}>
          <AppBarContext.Provider
            value={
              {
                selectedRemoteNode: 'local',
                workspaceSelection: { kind: WorkspaceKind.all },
              } as never
            }
          >
            <CommandPaletteProvider>
              <Routes>
                <Route path="*" element={<CurrentLocation />} />
              </Routes>
            </CommandPaletteProvider>
          </AppBarContext.Provider>
        </MemoryRouter>
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

async function openPalette() {
  fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
  return screen.findByRole('combobox', { name: 'Command palette' });
}

beforeEach(() => {
  useQueryMock.mockReset();
  useQueryMock.mockReturnValue({
    data: {
      dags: [
        {
          fileName: 'etl-daily.yaml',
          dag: { name: 'etl-daily', description: 'Load the warehouse' },
        },
      ],
    },
    isLoading: false,
  });
});

describe('CommandPalette', () => {
  it('opens with Ctrl+K and jumps to a workflow', async () => {
    renderPalette();

    const input = await openPalette();
    expect(screen.getByRole('option', { name: /etl-daily/ })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(screen.getByRole('status', { name: 'location' })).toHaveTextContent(
      '/dags/etl-daily.yaml'
    );
    expect(screen.getByRole('status', { name: 'location' }).textContent).toBe(
      buildDAGPageURL({ fileName: 'etl-daily.yaml', remoteNode: 'local' })
    );
  });

  it('filters pages and offers a content search for the query', async () => {
    renderPalette();

    const input = await openPalette();
    fireEvent.change(input, { target: { value: 'audit' } });

    expect(screen.getByRole('option', { name: /Audit Logs/ })).toBeVisible();
    expect(
      screen.queryByRole('option', { name: /Executions/ })
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('option', {
        name: 'Search workflow contents for "audit"',
      })
    );

    expect(screen.getByRole('status', { name: 'location' })).toHaveTextContent(
      '/search?q=audit'
    );
  });

  it('leaves Ctrl+K to the code editor when it has focus', () => {
    renderPalette();
    const editor = document.createElement('div');
    editor.className = 'monaco-editor';
    const textarea = document.createElement('textarea');
    editor.appendChild(textarea);
    document.body.appendChild(editor);

    fireEvent.keyDown(textarea, { key: 'k', ctrlKey: true });

    expect(
      screen.queryByRole('combobox', { name: 'Command palette' })
    ).not.toBeInTheDocument();
    editor.remove();
  });
});
