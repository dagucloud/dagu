// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useContext } from 'react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { type Config, ConfigContext } from '@/contexts/ConfigContext';
import { PeekContext } from '../PeekContext';
import { PeekHost } from '../PeekHost';
import type { PeekTarget } from '../peekParams';

vi.mock('../WorkflowPeek', async () => {
  const { DialogTitle } = await import('@/components/ui/dialog');
  return {
    WorkflowPeek: ({
      target,
      onClose,
      onSelectRun,
    }: {
      target: PeekTarget;
      onClose(): void;
      onSelectRun(run: { name: string; dagRunId: string }): void;
    }) => (
      <>
        <DialogTitle>{target.dag}</DialogTitle>
        <p>{target.run ? `run ${target.run}` : 'latest run'}</p>
        <button type="button" onClick={onClose}>
          Close
        </button>
        <button
          type="button"
          onClick={() => onSelectRun({ name: 'etl', dagRunId: 'run-2' })}
        >
          Older run
        </button>
      </>
    ),
  };
});

function OpenButton() {
  const peek = useContext(PeekContext);
  return (
    <button type="button" onClick={() => peek?.open({ dag: 'etl' })}>
      Open etl
    </button>
  );
}

function HistoryProbe() {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="location">{`${location.pathname}${location.search}`}</output>
      <button type="button" onClick={() => navigate(-1)}>
        Back
      </button>
    </>
  );
}

function renderHost(entries = ['/home', '/dags?view=all']) {
  return render(
    <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
      <ConfigContext.Provider value={{ basePath: '' } as Config}>
        <PeekHost>
          <OpenButton />
        </PeekHost>
        <HistoryProbe />
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

const location = () => screen.getByTestId('location').textContent;
// The modal sheet hides the rest of the page from the accessibility tree.
const back = () =>
  fireEvent.click(screen.getByRole('button', { name: 'Back', hidden: true }));

function openPeek() {
  fireEvent.click(screen.getByRole('button', { name: 'Open etl' }));
  return screen.getByRole('dialog', { name: 'etl' });
}

describe('PeekHost', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  // Each close leaves the entry that the open pushed: the next Back leaves
  // the list.
  it.each([
    [
      'Esc',
      (dialog: HTMLElement) => fireEvent.keyDown(dialog, { key: 'Escape' }),
    ],
    [
      'the close button',
      () => fireEvent.click(screen.getByRole('button', { name: 'Close' })),
    ],
    [
      'the backdrop',
      async () => {
        // The outside-press listener arms on the next tick.
        await act(() => new Promise((resolve) => setTimeout(resolve, 0)));
        fireEvent.pointerDown(document.body);
      },
    ],
  ])('closes on %s and pops its history entry', async (_, close) => {
    renderHost();
    const dialog = openPeek();
    expect(location()).toBe('/dags?view=all&peek=etl');

    await close(dialog);

    expect(location()).toBe('/dags?view=all');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    back();
    expect(location()).toBe('/home');
  });

  it('closes on Back', () => {
    renderHost();
    openPeek();

    back();

    expect(location()).toBe('/dags?view=all');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('closes a peek of a shared link in place', () => {
    renderHost(['/home', '/dags?view=all&peek=etl']);
    expect(screen.getByRole('dialog', { name: 'etl' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));

    expect(location()).toBe('/dags?view=all');
    back();
    expect(location()).toBe('/home');
  });

  it('shows another run in place of the shown one', () => {
    renderHost();
    openPeek();

    fireEvent.click(screen.getByRole('button', { name: 'Older run' }));

    expect(location()).toBe('/dags?view=all&peek=etl&peekRun=run-2');
    expect(screen.getByText('run run-2')).toBeInTheDocument();
    // The switch replaced the entry, so one close still pops it.
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(location()).toBe('/dags?view=all');
  });

  it.each(['Enter', 'f', 'F'])('opens the full page on %s', (key) => {
    renderHost();
    const dialog = openPeek();

    fireEvent.keyDown(dialog, { key });

    expect(location()).toBe('/dags/etl?remoteNode=local');
  });

  it('opens the full page in a new tab on Ctrl+Enter', () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null);
    renderHost();
    const dialog = openPeek();

    fireEvent.keyDown(dialog, { key: 'Enter', ctrlKey: true });

    expect(open).toHaveBeenCalledWith(
      '/dags/etl?remoteNode=local',
      '_blank',
      'noopener'
    );
    expect(location()).toBe('/dags?view=all&peek=etl');
  });

  it('leaves Enter on a button to the button', () => {
    renderHost();
    openPeek();

    fireEvent.keyDown(screen.getByRole('button', { name: 'Older run' }), {
      key: 'Enter',
    });

    expect(location()).toBe('/dags?view=all&peek=etl');
  });

  it('takes the focus into the sheet', () => {
    renderHost();
    const dialog = openPeek();

    expect(dialog).toHaveFocus();
  });
});
