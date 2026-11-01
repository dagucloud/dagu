// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { VisualCanvasProps } from '../../contracts';
import {
  catalogSample,
  detailsArraySteps,
  modelArraySteps,
  namesArraySteps,
} from '../../testing/fixtures';
import { ContextMenu } from '../ContextMenu';
import type { ContextMenuRow } from '../contextMenuItems';
import { buildCanvasGraph } from '../graph';
import { VisualCanvas } from '../VisualCanvas';

const rows: ContextMenuRow[] = [
  {
    id: 'viewLogs',
    label: 'View logs',
    shortcut: 'Enter',
    disabled: false,
    section: 'open',
  },
  {
    id: 'openSubRun',
    label: 'Open sub-run',
    shortcut: '',
    disabled: false,
    section: 'open',
  },
  {
    id: 'paste',
    label: 'Paste',
    shortcut: '',
    disabled: true,
    reason: 'Not available',
    section: 'edit',
  },
  {
    id: 'addStep',
    label: 'Add step',
    shortcut: 'F2',
    disabled: false,
    section: 'edit',
  },
  {
    id: 'fitView',
    label: 'Fit view',
    shortcut: 'Delete',
    disabled: false,
    section: 'edit',
  },
];

/** A button that opens the menu, as a toolbar button can. */
function Opener({ onClose }: { onClose?: (restore: boolean) => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Opener
      </button>
      <button type="button">Elsewhere</button>
      {open && (
        <ContextMenu
          rows={rows}
          x={10}
          y={10}
          label="Canvas menu"
          onSelect={vi.fn()}
          onClose={(restore) => {
            setOpen(false);
            onClose?.(restore);
          }}
        />
      )}
    </>
  );
}

function openFromButton() {
  const opener = screen.getByRole('button', { name: 'Opener' });
  opener.focus();
  fireEvent.click(opener);
  return opener;
}

const item = (name: string) => screen.getByRole('menuitem', { name });

describe('ContextMenu accessibility', () => {
  it('is a menu of menuitems that carry their key shortcuts', () => {
    render(<Opener />);
    openFromButton();
    const menu = screen.getByRole('menu', { name: 'Canvas menu' });
    const items = within(menu).getAllByRole('menuitem');
    expect(items.map((row) => row.getAttribute('aria-label'))).toEqual([
      'View logs',
      'Open sub-run',
      'Paste',
      'Add step',
      'Fit view',
    ]);
    expect(item('View logs')).toHaveAttribute('aria-keyshortcuts', 'Enter');
    expect(item('Add step')).toHaveAttribute('aria-keyshortcuts', 'F2');
    expect(item('Fit view')).toHaveAttribute('aria-keyshortcuts', 'Delete');
    expect(item('Open sub-run')).not.toHaveAttribute('aria-keyshortcuts');
  });

  it('moves one roving focus with Up, Down, Home and End', () => {
    render(<Opener />);
    openFromButton();
    const menu = screen.getByRole('menu');
    const tabbable = () =>
      within(menu)
        .getAllByRole('menuitem')
        .filter((row) => row.tabIndex === 0)
        .map((row) => row.getAttribute('aria-label'));

    expect(item('View logs')).toHaveFocus();
    expect(tabbable()).toEqual(['View logs']);
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(item('Open sub-run')).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(item('Add step')).toHaveFocus();
    expect(tabbable()).toEqual(['Add step']);
    fireEvent.keyDown(menu, { key: 'ArrowUp' });
    expect(item('Open sub-run')).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'End' });
    expect(item('Fit view')).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'Home' });
    expect(item('View logs')).toHaveFocus();
    expect(tabbable()).toEqual(['View logs']);
  });

  it('marks a blocked row with aria-disabled and describes the reason', () => {
    render(<Opener />);
    openFromButton();
    const copy = item('Paste');
    expect(copy).toHaveAttribute('aria-disabled', 'true');
    expect(copy).toHaveAccessibleDescription('Not available');
    expect(item('View logs')).not.toHaveAttribute('aria-disabled');
    expect(item('View logs')).not.toHaveAttribute('aria-describedby');
  });

  it.each(['Escape', 'Tab'])(
    'gives the focus back to the opener on %s',
    (key) => {
      const onClose = vi.fn();
      render(<Opener onClose={onClose} />);
      const opener = openFromButton();
      expect(item('View logs')).toHaveFocus();
      fireEvent.keyDown(screen.getByRole('menu'), { key });
      expect(screen.queryByRole('menu')).toBeNull();
      expect(onClose).toHaveBeenCalledWith(true);
      expect(opener).toHaveFocus();
    }
  );

  it('leaves the focus alone on a click outside', () => {
    render(<Opener />);
    openFromButton();
    const elsewhere = screen.getByRole('button', { name: 'Elsewhere' });
    fireEvent.pointerDown(elsewhere);
    elsewhere.focus();
    expect(screen.queryByRole('menu')).toBeNull();
    expect(elsewhere).toHaveFocus();
  });
});

describe('canvas menu focus return', () => {
  const graph = buildCanvasGraph({
    model: modelArraySteps,
    names: namesArraySteps,
    details: detailsArraySteps,
    issues: { byStep: {}, dag: [] },
  });

  function renderCanvas(overrides: Partial<VisualCanvasProps> = {}) {
    const props: VisualCanvasProps = {
      graph,
      direction: 'LR',
      selected: null,
      frozen: false,
      readOnly: false,
      catalog: catalogSample,
      onSelect: vi.fn(),
      onEdit: vi.fn(),
      onCommand: vi.fn(),
      onDirectionChange: vi.fn(),
      exportName: 'my-dag',
      ...overrides,
    };
    render(
      <UserPreferencesProvider>
        <VisualCanvas {...props} />
      </UserPreferencesProvider>
    );
  }

  it('returns the focus to the canvas pane that opened the menu', () => {
    renderCanvas();
    const pane = screen.getByTestId('ve-canvas');
    pane.focus();
    fireEvent.keyDown(pane, { key: 'F10', shiftKey: true });
    expect(screen.getByRole('menu')).toContainElement(
      document.activeElement as HTMLElement
    );
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
    expect(pane).toHaveFocus();
  });
});
