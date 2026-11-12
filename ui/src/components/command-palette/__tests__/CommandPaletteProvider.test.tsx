// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { useQuery } from '@/hooks/api';
import { I18nProvider } from '@/i18n/I18nProvider';
import { WorkspaceKind } from '@/lib/workspace';
import {
  CommandPaletteProvider,
  type PaletteCommand,
  usePaletteCommands,
} from '../CommandPaletteProvider';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));

vi.mock('@/hooks/useNavigationSections', () => ({
  useNavigationSections: () => ({ mainItems: [], sections: [] }),
}));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;

const GROUP = { id: 'workflow', label: 'This workflow' } as const;

function Registrar({ commands }: { commands: PaletteCommand[] | null }) {
  usePaletteCommands(GROUP, commands);
  return null;
}

let setMounted: (mounted: boolean) => void = () => undefined;

function Page({ commands }: { commands: PaletteCommand[] | null }) {
  const [mounted, set] = React.useState(true);
  setMounted = set;
  return mounted ? <Registrar commands={commands} /> : null;
}

function renderPalette(commands: PaletteCommand[] | null) {
  render(
    <UserPreferencesProvider>
      <I18nProvider>
        <MemoryRouter initialEntries={['/dags/x']}>
          <AppBarContext.Provider
            value={
              {
                selectedRemoteNode: 'local',
                workspaceSelection: { kind: WorkspaceKind.all },
              } as never
            }
          >
            <CommandPaletteProvider>
              <Page commands={commands} />
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

function command(
  id: string,
  extra: Partial<PaletteCommand> = {}
): PaletteCommand {
  return { id, label: `Command ${id}`, run: vi.fn(), ...extra };
}

beforeEach(() => {
  useQueryMock.mockReset();
  useQueryMock.mockReturnValue({ data: { dags: [] }, isLoading: false });
});

// parity:crit-command-palette-entry
describe('usePaletteCommands', () => {
  it('shows the group while the page is mounted', async () => {
    renderPalette([command('save')]);
    await openPalette();

    expect(screen.getByText('This workflow')).toBeInTheDocument();
    expect(
      screen.getByRole('option', { name: /Command save/ })
    ).toBeInTheDocument();

    act(() => setMounted(false));

    expect(screen.queryByText('This workflow')).not.toBeInTheDocument();
    expect(
      screen.queryByRole('option', { name: /Command save/ })
    ).not.toBeInTheDocument();
  });

  it('registers nothing for null', async () => {
    renderPalette(null);
    await openPalette();
    expect(screen.queryByText('This workflow')).not.toBeInTheDocument();
  });

  it('runs a command and closes the palette', async () => {
    const save = command('save');
    renderPalette([save]);
    const input = await openPalette();

    fireEvent.keyDown(input, { key: 'Enter' });

    expect(save.run).toHaveBeenCalledTimes(1);
    expect(
      screen.queryByRole('combobox', { name: 'Command palette' })
    ).not.toBeInTheDocument();
  });

  it('filters the commands by label and keywords', async () => {
    renderPalette([
      command('save'),
      command('step', { label: 'Go to step', keywords: ['extract'] }),
    ]);
    const input = await openPalette();

    fireEvent.change(input, { target: { value: 'extract' } });

    expect(screen.getByRole('option', { name: 'Go to step' })).toBeVisible();
    expect(
      screen.queryByRole('option', { name: /Command save/ })
    ).not.toBeInTheDocument();
  });

  it('shows a disabled command that does not run', async () => {
    const undo = command('undo', { disabled: true });
    renderPalette([undo]);
    const input = await openPalette();

    const option = screen.getByRole('option', { name: /Command undo/ });
    expect(option).toHaveAttribute('aria-disabled', 'true');
    fireEvent.click(option);
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(undo.run).not.toHaveBeenCalled();
  });

  it('reads available() at each opening', async () => {
    let present = false;
    renderPalette([command('stop', { available: () => present })]);
    const input = await openPalette();
    expect(
      screen.queryByRole('option', { name: /Command stop/ })
    ).not.toBeInTheDocument();

    fireEvent.keyDown(input, { key: 'Escape' });
    present = true;
    await openPalette();

    expect(
      screen.getByRole('option', { name: /Command stop/ })
    ).toBeInTheDocument();
  });

  // The active option is named by its DOM id: a workflow whose file name is
  // a command id, or a second registration, must not share it.
  it('gives every option its own id', async () => {
    useQueryMock.mockReturnValue({
      data: { dags: [{ fileName: 'save', dag: { name: 'save' } }] },
      isLoading: false,
    });
    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <MemoryRouter initialEntries={['/dags/x']}>
            <AppBarContext.Provider
              value={
                {
                  selectedRemoteNode: 'local',
                  workspaceSelection: { kind: WorkspaceKind.all },
                } as never
              }
            >
              <CommandPaletteProvider>
                <Registrar commands={[command('save')]} />
                <Registrar commands={[command('save')]} />
              </CommandPaletteProvider>
            </AppBarContext.Provider>
          </MemoryRouter>
        </I18nProvider>
      </UserPreferencesProvider>
    );
    await openPalette();

    const ids = screen
      .getAllByRole('option', { name: /save/ })
      .map((option) => option.id);
    expect(ids).toHaveLength(3);
    expect(new Set(ids).size).toBe(3);
  });

  it('shows the shortcut text of a command', async () => {
    renderPalette([command('refresh', { shortcut: 'R' })]);
    await openPalette();
    const option = screen.getByRole('option', { name: /Command refresh/ });
    expect(within(option).getByText('R').tagName).toBe('KBD');
  });

  it('leaves Ctrl+K to terminals and code editors', () => {
    renderPalette([command('save')]);
    for (const className of ['monaco-editor', 'xterm', 'cm-editor']) {
      const owner = document.createElement('div');
      owner.className = className;
      const textarea = document.createElement('textarea');
      owner.appendChild(textarea);
      document.body.appendChild(owner);

      fireEvent.keyDown(textarea, { key: 'k', ctrlKey: true });

      expect(
        screen.queryByRole('combobox', { name: 'Command palette' })
      ).not.toBeInTheDocument();
      owner.remove();
    }
  });
});
