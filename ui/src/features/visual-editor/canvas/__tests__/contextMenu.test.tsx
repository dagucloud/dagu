// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type {
  CanvasGraph,
  StepCommandGates,
  VisualCanvasProps,
} from '../../contracts';
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

const base = buildCanvasGraph({
  model: modelArraySteps,
  names: namesArraySteps,
  details: detailsArraySteps,
  issues: { byStep: {}, dag: [] },
});
// The last step has a run, so that its menu offers the logs.
const graph: CanvasGraph = {
  ...base,
  nodes: base.nodes.map((node) =>
    node.key === 'steps[1]'
      ? {
          ...node,
          run: {
            status: NodeStatus.Failed,
            statusLabel: 'failed',
            retryCount: 0,
            doneCount: 0,
          },
        }
      : node
  ),
};

const gates: StepCommandGates = {
  canRunStep: true,
  canRetry: () => true,
  canSetStatus: () => true,
  hasSubRuns: () => true,
};

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
    stepCommandGates: gates,
    ...overrides,
  };
  const view = render(
    <UserPreferencesProvider>
      <VisualCanvas {...props} />
    </UserPreferencesProvider>
  );
  return { ...view, props };
}

function nodeBox(key: string): HTMLElement {
  const element = Array.from(
    document.querySelectorAll<HTMLElement>('[data-flow-node]')
  ).find((candidate) => candidate.dataset.flowNode === key);
  if (!element) {
    throw new Error(`no node ${key}`);
  }
  return element;
}

function rightClick(target: Element) {
  fireEvent.contextMenu(target, { clientX: 10, clientY: 20 });
}

function pick(label: string) {
  fireEvent.click(screen.getByRole('menuitem', { name: label }));
}

function rowLabels(): string[] {
  return screen
    .getAllByRole('menuitem')
    .concat(screen.queryAllByRole('menuitemradio'))
    .map((row) => row.getAttribute('aria-label') ?? '');
}

const rows: ContextMenuRow[] = [
  {
    id: 'viewLogs',
    label: 'View logs',
    shortcut: 'Enter',
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

describe('ContextMenu', () => {
  function renderMenu() {
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(
      <ContextMenu
        rows={rows}
        x={10}
        y={10}
        label="Canvas menu"
        onSelect={onSelect}
        onClose={onClose}
      />
    );
    return { onSelect, onClose };
  }

  it('is a menu whose rows show their keys', () => {
    renderMenu();
    expect(screen.getByRole('menu', { name: 'Canvas menu' })).toBeVisible();
    const open = screen.getByRole('menuitem', { name: 'View logs' });
    expect(open).toHaveTextContent('Enter');
    expect(open).toHaveAttribute('aria-keyshortcuts', 'Enter');
    expect(
      screen.getByRole('menuitem', { name: /Add step/ })
    ).toHaveTextContent('F2');
    expect(open).toHaveFocus();
    expect(screen.getAllByRole('separator')).toHaveLength(1);
  });

  it('moves with the arrows, Home and End, and skips disabled rows', () => {
    const { onSelect } = renderMenu();
    const menu = screen.getByRole('menu');
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(screen.getByRole('menuitem', { name: /Add step/ })).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'End' });
    expect(screen.getByRole('menuitem', { name: /Fit view/ })).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(screen.getByRole('menuitem', { name: /View logs/ })).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'ArrowUp' });
    expect(screen.getByRole('menuitem', { name: /Fit view/ })).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'Home' });
    fireEvent.keyDown(menu, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith('viewLogs');
  });

  it('shows the reason of a disabled row and does not select it', () => {
    const { onSelect } = renderMenu();
    const copy = screen.getByRole('menuitem', { name: /Paste/ });
    expect(copy).toHaveAttribute('aria-disabled', 'true');
    expect(copy).toHaveAttribute('title', 'Not available');
    fireEvent.click(copy);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('closes on Esc and gives the focus back', () => {
    const { onClose } = renderMenu();
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledWith(true);
  });

  it('closes on a click outside, without taking the focus', () => {
    const { onClose } = renderMenu();
    fireEvent.pointerDown(document.body);
    expect(onClose).toHaveBeenCalledWith(false);
  });

  // A run update can add rows while the menu is open: Enter must still
  // select the focused row.
  it('keeps the focused row when rows change while it is open', () => {
    const onSelect = vi.fn();
    const props = {
      x: 10,
      y: 10,
      label: 'Canvas menu',
      onSelect,
      onClose: vi.fn(),
    };
    const { rerender } = render(<ContextMenu rows={rows} {...props} />);
    const menu = screen.getByRole('menu');
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(screen.getByRole('menuitem', { name: /Add step/ })).toHaveFocus();
    const retry: ContextMenuRow = {
      id: 'retryStep',
      label: 'Retry step',
      shortcut: '',
      disabled: false,
      section: 'run',
    };
    rerender(
      <ContextMenu rows={[rows[0]!, retry, ...rows.slice(1)]} {...props} />
    );
    expect(screen.getByRole('menuitem', { name: /Add step/ })).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith('addStep');
  });

  it('moves the focus to a row that stays when the active row goes', () => {
    const props = {
      x: 10,
      y: 10,
      label: 'Canvas menu',
      onSelect: vi.fn(),
      onClose: vi.fn(),
    };
    const { rerender } = render(<ContextMenu rows={rows} {...props} />);
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'End' });
    expect(screen.getByRole('menuitem', { name: /Fit view/ })).toHaveFocus();
    rerender(<ContextMenu rows={rows.slice(0, 3)} {...props} />);
    expect(screen.getByRole('menuitem', { name: 'View logs' })).toHaveFocus();
  });
});

describe('canvas menus', () => {
  // parity:graph-rightclick-status parity:run-graph-rightclick-status
  // parity:steps-status-update-modal parity:history-status-update
  // parity:api-step-status parity:chg-history-status-gate
  // One gate, stepStatusGate through stepCommandGates.canSetStatus, serves
  // the Editor and Executions.
  it("emits 'Set status…' as a step command with the server name", () => {
    const { props } = renderCanvas();
    rightClick(nodeBox('steps[1]'));
    pick('Change status');
    expect(props.onCommand).toHaveBeenCalledWith({
      kind: 'step',
      cmd: { kind: 'setStatus', step: 'transform' },
    });
    expect(screen.queryByRole('menu')).toBeNull();
  });

  it.each([
    ['Retry step', 'retryStep'],
    ['Open sub-run', 'openSubRun'],
    ['View logs', 'viewLogs'],
  ] as const)('emits %s as a step command', (label, kind) => {
    const { props } = renderCanvas();
    rightClick(nodeBox('steps[1]'));
    pick(label);
    expect(props.onCommand).toHaveBeenCalledWith({
      kind: 'step',
      cmd: { kind, step: 'transform' },
    });
  });

  // Open, Run step, Duplicate, Copy and Delete are on the hover toolbar of
  // the node and have their keys: the node menu has no row for them.
  it('has no row that the node toolbar already has', () => {
    renderCanvas();
    rightClick(nodeBox('steps[0]'));
    const labels = rowLabels();
    for (const gone of [
      'Open',
      'Open step',
      'Run step',
      'Copy',
      'Duplicate',
      'Delete',
    ]) {
      expect(labels).not.toContain(gone);
    }
  });

  it('shows no edit row and no run row to a read-only user', () => {
    const { props } = renderCanvas({
      readOnly: true,
      stepCommandGates: {
        canRunStep: false,
        canRetry: () => false,
        canSetStatus: () => false,
        hasSubRuns: () => false,
      },
    });
    rightClick(nodeBox('steps[1]'));
    expect(rowLabels()).toEqual(['View logs']);
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
    expect(props.onEdit).not.toHaveBeenCalled();
  });

  it('removes a dependency and inserts a step from the edge menu', () => {
    const onAddStep = vi.fn();
    const { props } = renderCanvas({ onAddStep });
    const edge = document.querySelector(
      '[data-flow-edge="depends:steps[0]->steps[1]"]'
    )!;
    rightClick(edge);
    pick('Remove dependency');
    expect(props.onEdit).toHaveBeenCalledWith({
      kind: 'removeDependency',
      step: 'steps[1]',
      dependsOn: 'steps[0]',
    });
    rightClick(edge);
    pick('Insert step');
    expect(onAddStep).toHaveBeenCalledWith({
      mode: 'insert',
      edgeId: 'depends:steps[0]->steps[1]',
    });
  });

  it('enables Paste only when the host has something to paste', () => {
    const view = renderCanvas();
    rightClick(screen.getByTestId('flow'));
    expect(screen.getByRole('menuitem', { name: 'Paste' })).toHaveAttribute(
      'aria-disabled',
      'true'
    );
    view.unmount();

    renderCanvas({ canPaste: true });
    rightClick(screen.getByTestId('flow'));
    expect(screen.getByRole('menuitem', { name: 'Paste' })).not.toHaveAttribute(
      'aria-disabled',
      'true'
    );
  });

  it('pastes the text of the clipboard from the pane menu', async () => {
    const readText = vi.fn(async () => '- name: x\n  run: echo x\n');
    vi.stubGlobal('navigator', { ...navigator, clipboard: { readText } });
    try {
      const { props } = renderCanvas({ canPaste: true });
      rightClick(screen.getByTestId('flow'));
      await act(async () => {
        pick('Paste');
      });
      expect(readText).toHaveBeenCalled();
      expect(props.onCommand).toHaveBeenLastCalledWith({
        kind: 'paste',
        text: '- name: x\n  run: echo x\n',
      });
    } finally {
      vi.unstubAllGlobals();
    }
  });

  // Without the clipboard text the host pastes the steps it copied last.
  it('pastes an empty text when the browser does not give the clipboard', async () => {
    const readText = vi.fn(async () => {
      throw new Error('denied');
    });
    vi.stubGlobal('navigator', { ...navigator, clipboard: { readText } });
    try {
      const { props } = renderCanvas({ canPaste: true });
      rightClick(screen.getByTestId('flow'));
      await act(async () => {
        pick('Paste');
      });
      expect(props.onCommand).toHaveBeenLastCalledWith({
        kind: 'paste',
        text: '',
      });
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('changes the direction from the pane menu', () => {
    const { props } = renderCanvas();
    rightClick(screen.getByTestId('flow'));
    fireEvent.click(
      screen.getByRole('menuitemradio', { name: /Top to bottom/ })
    );
    expect(props.onDirectionChange).toHaveBeenCalledWith('TB');
  });

  it('opens the key help from the pane menu', () => {
    renderCanvas();
    rightClick(screen.getByTestId('flow'));
    pick('Keyboard shortcuts');
    expect(
      screen.getByRole('dialog', { name: 'Keyboard shortcuts' })
    ).toBeInTheDocument();
  });

  it('gives the focus back to the canvas on Esc', () => {
    renderCanvas();
    rightClick(nodeBox('steps[0]'));
    expect(screen.getByRole('menu')).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
    expect(screen.queryByRole('menu')).toBeNull();
    expect(screen.getByTestId('ve-canvas')).toHaveFocus();
  });

  it('opens the menu of the selected step with the menu key', () => {
    renderCanvas({ selected: 'steps[2]' });
    fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key: 'ContextMenu' });
    expect(rowLabels()).toContain('Add step');
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
    fireEvent.keyDown(screen.getByTestId('ve-canvas'), {
      key: 'F10',
      shiftKey: true,
    });
    expect(screen.getByRole('menu')).toBeInTheDocument();
  });

  describe('export', () => {
    let downloads: string[];
    let saved: [unknown, unknown];

    beforeEach(() => {
      downloads = [];
      saved = [URL.createObjectURL, URL.revokeObjectURL];
      URL.createObjectURL = vi.fn(() => 'blob:x');
      URL.revokeObjectURL = vi.fn();
      vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(
        function (this: HTMLAnchorElement) {
          downloads.push(this.download);
        }
      );
    });

    afterEach(() => {
      URL.createObjectURL = saved[0] as typeof URL.createObjectURL;
      URL.revokeObjectURL = saved[1] as typeof URL.revokeObjectURL;
      vi.restoreAllMocks();
    });

    // parity:chg-export-filename parity:graph-export parity:canvas-export
    // parity:run-graph-export parity:test-ve-export
    it("saves the graph as '{dagName}.svg' from the pane menu", () => {
      renderCanvas();
      rightClick(screen.getByTestId('flow'));
      act(() => {
        pick('Export as SVG');
      });
      expect(downloads).toEqual(['my-dag.svg']);
    });
  });
});
