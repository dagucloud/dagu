// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CanvasToolbar } from '../CanvasToolbar';

// The controls that VisualEditor gives as toolbarEnd.
function toolbarEnd(onUndo = vi.fn()) {
  return (
    <>
      <button type="button" aria-label="Undo" onClick={onUndo} />
      <button type="button" aria-label="Redo" />
      <button type="button" aria-pressed={false}>
        Workflow
      </button>
      <button type="button" aria-pressed={false}>
        YAML
      </button>
      <button type="button">Issues</button>
      <button type="button">Save</button>
    </>
  );
}

// parity:toolbar-layout
describe('CanvasToolbar', () => {
  // The DOM before the compact prop existed, so hosts without it see no change.
  it('keeps its DOM without compact', () => {
    const { container } = render(
      <CanvasToolbar
        direction="LR"
        onDirectionChange={vi.fn()}
        toolbarEnd={toolbarEnd()}
      />
    );
    expect(container.innerHTML).toMatchInlineSnapshot(
      `"<div class="flex items-center gap-1 rounded-md border bg-card p-0.5 shadow-sm"><button type="button" aria-label="Left to right" title="Re-align the steps" aria-pressed="true" class="flex h-7 items-center gap-1 rounded px-1.5 text-xs hover:bg-muted hover:text-foreground disabled:pointer-events-none disabled:opacity-50 bg-muted text-foreground"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-arrow-right h-3.5 w-3.5" aria-hidden="true"><path d="M5 12h14"></path><path d="m12 5 7 7-7 7"></path></svg></button><button type="button" aria-label="Top to bottom" title="Top to bottom" aria-pressed="false" class="flex h-7 items-center gap-1 rounded px-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:pointer-events-none disabled:opacity-50"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-arrow-down h-3.5 w-3.5" aria-hidden="true"><path d="M12 5v14"></path><path d="m19 12-7 7-7-7"></path></svg></button></div>"`
    );
  });

  // The toolbar of a host floats over the graph: the container has no
  // background, border or shadow; the host gives each button its chrome.
  it('has no background, border or shadow when it floats', () => {
    const { container } = render(
      <CanvasToolbar direction="LR" onDirectionChange={vi.fn()} floating />
    );
    const bar = container.firstElementChild as HTMLElement;
    expect(bar).toHaveClass('flex', 'items-center', 'gap-1');
    for (const token of ['border', 'bg-card', 'shadow-sm', 'p-0.5']) {
      expect(bar).not.toHaveClass(token);
    }
  });

  // A run is read-only: the direction and the chip stay.
  it('keeps the directions and the chip for a reader', () => {
    render(
      <CanvasToolbar direction="LR" onDirectionChange={vi.fn()} readOnly />
    );
    expect(screen.queryByRole('button', { name: 'Add step' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Left to right' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Top to bottom' })).toBeEnabled();
    expect(screen.getByText('Read-only')).toBeInTheDocument();
  });

  it('changes the direction inline and has no Add step', () => {
    const onDirectionChange = vi.fn();
    render(
      <CanvasToolbar direction="TB" onDirectionChange={onDirectionChange} />
    );
    expect(screen.queryByRole('button', { name: 'Add step' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Left to right' }));
    expect(onDirectionChange).toHaveBeenCalledWith('LR');
    expect(screen.queryByRole('button', { name: 'Tools' })).toBeNull();
  });
});

describe('CanvasToolbar trailing controls', () => {
  it('puts them in the row of the directions, after a separator', () => {
    render(
      <CanvasToolbar
        direction="LR"
        onDirectionChange={vi.fn()}
        trailing={<button type="button">Zoom in</button>}
      />
    );
    const buttons = screen
      .getAllByRole('button')
      .map((b) => b.ariaLabel ?? b.textContent);
    expect(buttons).toEqual(['Left to right', 'Top to bottom', 'Zoom in']);
  });
});

describe('CanvasToolbar compact', () => {
  it('has no Add step button and keeps the rest in a Tools sheet', () => {
    render(
      <CanvasToolbar
        compact
        direction="TB"
        onDirectionChange={vi.fn()}
        toolbarEnd={toolbarEnd()}
      />
    );
    expect(screen.queryByTestId('ve-add-step-fab')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Add step' })).toBeNull();

    expect(screen.getByRole('button', { name: 'Tools' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
    // Nothing else shows until the sheet opens.
    for (const name of ['Top to bottom', 'Undo', 'Save']) {
      expect(screen.queryByRole('button', { name })).toBeNull();
    }
  });

  it('shows the read-only chip and the Tools button for a reader', () => {
    render(
      <CanvasToolbar
        compact
        readOnly
        direction="TB"
        onDirectionChange={vi.fn()}
      />
    );
    expect(screen.getByText('Read-only')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Tools' })).toBeInTheDocument();
  });

  it('holds the directions and the end controls in the sheet', () => {
    const onDirectionChange = vi.fn();
    render(
      <CanvasToolbar
        compact
        direction="TB"
        onDirectionChange={onDirectionChange}
        toolbarEnd={toolbarEnd()}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Tools' }));
    const sheet = screen.getByRole('dialog', { name: 'Canvas tools' });
    expect(sheet).toHaveAttribute('data-testid', 've-tools-sheet');
    const directions = within(sheet).getByRole('group', {
      name: 'Layout direction',
    });
    expect(
      within(directions).getByRole('button', { name: 'Top to bottom' })
    ).toHaveAttribute('aria-pressed', 'true');
    for (const name of ['Undo', 'Redo', 'Workflow', 'YAML', 'Issues', 'Save']) {
      expect(
        within(screen.getByTestId('ve-tools-sheet-actions')).getByRole(
          'button',
          { name }
        )
      ).toBeInTheDocument();
    }

    fireEvent.click(
      within(directions).getByRole('button', { name: 'Left to right' })
    );
    expect(onDirectionChange).toHaveBeenCalledWith('LR');
    // A toggle changes the view under the sheet: the sheet closes.
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('stays open for Undo and closes for a panel toggle', () => {
    const onUndo = vi.fn();
    render(
      <CanvasToolbar
        compact
        direction="TB"
        onDirectionChange={vi.fn()}
        toolbarEnd={toolbarEnd(onUndo)}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Tools' }));
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    expect(onUndo).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'YAML' }));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('returns the focus to Tools when the sheet closes', async () => {
    render(
      <CanvasToolbar
        compact
        direction="TB"
        onDirectionChange={vi.fn()}
        toolbarEnd={toolbarEnd()}
      />
    );
    const tools = screen.getByRole('button', { name: 'Tools' });
    tools.focus();
    fireEvent.click(tools);
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
    // The focus moves after the sheet unmounts, in the next task.
    await act(() => new Promise((resolve) => setTimeout(resolve, 0)));
    expect(tools).toHaveFocus();
  });

  // The Issues popover of the sheet opens in a portal as role=dialog.
  it('stays open for a layer that a control of the sheet opens', async () => {
    render(
      <>
        <CanvasToolbar
          compact
          direction="TB"
          onDirectionChange={vi.fn()}
          toolbarEnd={toolbarEnd()}
        />
        <div role="dialog" aria-label="Issues">
          <button type="button">An issue</button>
        </div>
        <button type="button">Elsewhere</button>
      </>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Tools' }));
    // The sheet listens for outside pointers from the next task on.
    await act(() => new Promise((resolve) => setTimeout(resolve, 0)));
    fireEvent.pointerDown(screen.getByRole('button', { name: 'An issue' }));
    expect(
      screen.getByRole('dialog', { name: 'Canvas tools' })
    ).toBeInTheDocument();

    fireEvent.pointerDown(screen.getByRole('button', { name: 'Elsewhere' }));
    expect(screen.queryByRole('dialog', { name: 'Canvas tools' })).toBeNull();
  });
});

describe('CanvasToolbar sheet', () => {
  // The sheet covers the canvas only: not the sidebar, not the window.
  it('opens the tools sheet inside the canvas', () => {
    render(
      <div data-testid="ve-canvas">
        <CanvasToolbar compact direction="LR" onDirectionChange={vi.fn()} />
      </div>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Tools' }));
    const sheet = screen.getByTestId('ve-tools-sheet');
    expect(screen.getByTestId('ve-canvas')).toContainElement(sheet);
    expect(sheet.className).toContain('absolute');
    expect(sheet.className).not.toContain('fixed');
  });
});
