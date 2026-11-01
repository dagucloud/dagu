// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createRef } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { CanvasViewHandle, VisualCanvasProps } from '../../contracts';
import {
  catalogSample,
  detailsArraySteps,
  modelArraySteps,
  namesArraySteps,
} from '../../testing/fixtures';
import { launcherStates } from '../../launchers/model';
import { useCanvasActions, type CanvasActions } from '../context';
import { CanvasOverlayInsetContext } from '../overlayInset';
import { buildCanvasGraph } from '../graph';
import { CANVAS_DETAILS_KEY } from '../useCanvasDetails';
import { agentColumns } from '../layout';
import { neighbourStep } from '../neighbours';
import type { CanvasKeyHandlers } from '../useCanvasKeymap';
import { EDGE_HOVER_SETTLE_MS } from '../engine/edgeParts';
import { VisualCanvas } from '../VisualCanvas';
import { makeModel, makeNames, makeStep } from './fixtures';

// The keys of copy, paste, zoom and arrows are bound later; the tests call
// the handlers that the canvas gives to the keymap.
const keymap = vi.hoisted(() => ({
  handlers: null as CanvasKeyHandlers | null,
}));
vi.mock('../useCanvasKeymap', async (importOriginal) => {
  const original = await importOriginal<typeof import('../useCanvasKeymap')>();
  return {
    ...original,
    useCanvasKeymap: (...args: Parameters<typeof original.useCanvasKeymap>) => {
      keymap.handlers = args[1];
      original.useCanvasKeymap(...args);
    },
  };
});
vi.mock('../neighbours', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../neighbours')>()),
  neighbourStep: vi.fn(() => null),
}));
// Counts the user moves that reach the auto-fit of the canvas.
const autoFit = vi.hoisted(() => ({ moves: 0 }));
vi.mock('../useAutoFit', async (importOriginal) => {
  const original = await importOriginal<typeof import('../useAutoFit')>();
  return {
    ...original,
    useAutoFit: (...args: Parameters<typeof original.useAutoFit>) => {
      const fit = original.useAutoFit(...args);
      return {
        ...fit,
        onUserMove: () => {
          autoFit.moves += 1;
          fit.onUserMove();
        },
      };
    },
  };
});

function keyHandlers(): CanvasKeyHandlers {
  if (!keymap.handlers) {
    throw new Error('no key handlers');
  }
  return keymap.handlers;
}

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
    ...overrides,
  };
  const view = render(
    <UserPreferencesProvider>
      <VisualCanvas {...props} />
    </UserPreferencesProvider>
  );
  return { ...view, props };
}

describe('VisualCanvas keyboard access', () => {
  // The canvas is a tab stop: it shows a focus ring and says how it works.
  it('shows a focus ring and describes its keys', () => {
    renderCanvas();
    const region = screen.getByRole('region', { name: 'Workflow canvas' });
    expect(region.className).toContain('focus-visible:ring-2');
    const hint = document.getElementById(
      region.getAttribute('aria-describedby') ?? ''
    );
    expect(hint).toHaveTextContent('Arrow keys select a step, Enter opens it');
  });
});

function pressKey(key: string) {
  fireEvent.keyDown(screen.getByTestId('ve-canvas'), { key });
}

function nodeWrapper(key: string): HTMLElement {
  const wrapper = nodeCard(key).closest<HTMLElement>('[data-flow-node]');
  if (!wrapper) {
    throw new Error(`no node wrapper for ${key}`);
  }
  return wrapper;
}

function edgeElement(id: string): HTMLElement {
  const element = document.querySelector<HTMLElement>(
    `[data-flow-edge="${id}"]`
  );
  if (!element) {
    throw new Error(`no edge ${id}`);
  }
  return element;
}

// Some tests hover with fake timers.
afterEach(() => {
  vi.useRealTimers();
});

// The buttons of an edge mount after a short hover.
function hoverEdge(id: string) {
  fireEvent.mouseEnter(
    edgeElement(id).querySelector('path[data-flow-edge-interaction]')!
  );
  act(() => vi.advanceTimersByTime(EDGE_HOVER_SETTLE_MS));
}

function nodeCard(key: string): HTMLElement {
  return screen.getByTestId(`ve-canvas-node-${key}`);
}

function pointer(
  target: Element | Window,
  type: 'pointerdown' | 'pointermove' | 'pointerup',
  x: number,
  y: number
) {
  act(() => {
    target.dispatchEvent(
      new MouseEvent(type, {
        bubbles: true,
        cancelable: true,
        clientX: x,
        clientY: y,
        button: 0,
      })
    );
  });
}

const emptyGraph = { nodes: [], edges: [], editableEdges: true };

describe('VisualCanvas', () => {
  it('shows one card for each step', () => {
    renderCanvas();
    for (const node of graph.nodes) {
      expect(nodeCard(node.key)).toBeInTheDocument();
    }
  });

  it('selects a step on click and clears on the pane', () => {
    const { props, container } = renderCanvas();
    fireEvent.click(nodeCard('steps[1]'));
    expect(props.onSelect).toHaveBeenLastCalledWith('steps[1]');
    const pane = container.querySelector('[data-flow-root]');
    expect(pane).not.toBeNull();
    fireEvent.click(pane as Element);
    expect(props.onSelect).toHaveBeenLastCalledWith(null);
  });

  it('opens the inspector on double click', () => {
    const { props } = renderCanvas();
    fireEvent.doubleClick(nodeCard('steps[0]'));
    expect(props.onCommand).toHaveBeenCalledWith({
      kind: 'openInspector',
      step: 'steps[0]',
    });
  });

  it('never emits an edit on its own', () => {
    const { props } = renderCanvas({ selected: 'steps[1]' });
    expect(props.onEdit).not.toHaveBeenCalled();
  });

  it('switches the layout direction', () => {
    const { props } = renderCanvas();
    const lr = screen.getByLabelText('Left to right');
    expect(lr).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByLabelText('Top to bottom'));
    expect(props.onDirectionChange).toHaveBeenCalledWith('TB');
  });

  // A click on the direction that is already active aligns the steps again:
  // the cards that the user moved return to the automatic layout.
  it('aligns the steps again when the active direction is clicked', () => {
    const { props } = renderCanvas();
    const before = nodeWrapper('steps[2]').style.transform;
    pointer(nodeCard('steps[2]'), 'pointerdown', 10, 10);
    pointer(window, 'pointermove', 210, 160);
    pointer(window, 'pointerup', 210, 160);
    const moved = nodeWrapper('steps[2]').style.transform;
    expect(moved).not.toBe(before);

    const lr = screen.getByLabelText('Left to right');
    expect(lr).toHaveAttribute('title', 'Re-align the steps');
    // A real click starts with a pointer down, which ends a drag.
    fireEvent.pointerDown(lr);
    fireEvent.click(lr);
    expect(nodeWrapper('steps[2]').style.transform).toBe(before);
    // The direction did not change: the host hears nothing.
    expect(props.onDirectionChange).not.toHaveBeenCalled();

    // The other direction is a change for the host.
    fireEvent.pointerDown(screen.getByLabelText('Top to bottom'));
    fireEvent.click(screen.getByLabelText('Top to bottom'));
    expect(props.onDirectionChange).toHaveBeenCalledWith('TB');
  });

  it('keeps positions when only a label changes', () => {
    const { props, rerender } = renderCanvas();
    const before = nodeWrapper('steps[2]').style.transform;
    const renamed = {
      ...graph,
      nodes: graph.nodes.map((node) =>
        node.key === 'steps[1]' ? { ...node, label: 'renamed' } : node
      ),
    };
    rerender(
      <UserPreferencesProvider>
        <VisualCanvas {...props} graph={renamed} />
      </UserPreferencesProvider>
    );
    expect(nodeWrapper('steps[2]').style.transform).toBe(before);
    rerender(
      <UserPreferencesProvider>
        <VisualCanvas {...props} graph={renamed} direction="TB" />
      </UserPreferencesProvider>
    );
    expect(nodeWrapper('steps[2]').style.transform).not.toBe(before);
  });

  it('makes handles connectable only when edges are editable', () => {
    const { container, rerender, props } = renderCanvas();
    const connectable = () =>
      container.querySelectorAll('[data-flow-handle][data-connectable="true"]')
        .length;
    expect(connectable()).toBeGreaterThan(0);
    rerender(
      <UserPreferencesProvider>
        <VisualCanvas {...props} readOnly />
      </UserPreferencesProvider>
    );
    expect(connectable()).toBe(0);
    rerender(
      <UserPreferencesProvider>
        <VisualCanvas {...props} graph={{ ...graph, editableEdges: false }} />
      </UserPreferencesProvider>
    );
    expect(connectable()).toBe(0);
  });

  it('deletes the selected step with rewire', () => {
    const { props } = renderCanvas({ selected: 'steps[1]' });
    pressKey('Delete');
    expect(props.onEdit).toHaveBeenCalledWith({
      kind: 'deleteStep',
      step: 'steps[1]',
      rewire: true,
    });
  });

  it('opens the selected step on Enter and clears on Escape', () => {
    const { props } = renderCanvas({ selected: 'steps[2]' });
    pressKey('Enter');
    expect(props.onCommand).toHaveBeenCalledWith({
      kind: 'openInspector',
      step: 'steps[2]',
    });
    pressKey('Escape');
    expect(props.onSelect).toHaveBeenLastCalledWith(null);
  });

  // The host closes its open details view on Escape: the canvas must not
  // consume the key first.
  it('leaves Escape to the host with escapeToHost', () => {
    const { props } = renderCanvas({
      selected: 'steps[2]',
      escapeToHost: true,
    });
    const passed = fireEvent.keyDown(screen.getByTestId('ve-canvas'), {
      key: 'Escape',
    });
    expect(passed).toBe(true);
    expect(props.onSelect).not.toHaveBeenCalled();
  });

  it('stops the auto-fit when a zoom button moves the view', () => {
    renderCanvas();
    autoFit.moves = 0;
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    fireEvent.click(screen.getByTestId('flow-zoom-reset'));
    expect(autoFit.moves).toBe(2);
    fireEvent.click(screen.getByRole('button', { name: 'Fit view' }));
    expect(autoFit.moves).toBe(2);
  });

  // A click on a link neither selects it nor changes the selected step: the
  // panels that follow the step stay as they are. The link is removed from
  // its own button or its menu.
  it('keeps the selected step when a dependency edge is clicked', () => {
    const { props } = renderCanvas({ selected: 'steps[2]' });
    fireEvent.click(edgeElement('depends:steps[0]->steps[1]'));
    expect(props.onSelect).not.toHaveBeenCalled();
    pressKey('Backspace');
    expect(props.onEdit).toHaveBeenCalledWith({
      kind: 'deleteStep',
      step: 'steps[2]',
      rewire: true,
    });
    expect(props.onEdit).not.toHaveBeenCalledWith(
      expect.objectContaining({ kind: 'removeDependency' })
    );
  });

  it('never deletes an inferred edge', () => {
    const { props } = renderCanvas();
    fireEvent.click(edgeElement('inferred:steps[0]->steps[2]'));
    pressKey('Delete');
    expect(props.onEdit).not.toHaveBeenCalled();
  });

  it('runs and deletes a step from its toolbar', () => {
    const { props } = renderCanvas();
    fireEvent.pointerEnter(nodeWrapper('steps[1]'));
    const toolbar = within(nodeWrapper('steps[1]'));
    fireEvent.click(toolbar.getByLabelText('Run step'));
    expect(props.onCommand).toHaveBeenCalledWith({
      kind: 'runStep',
      step: 'steps[1]',
    });
    fireEvent.click(toolbar.getByLabelText('Delete'));
    expect(props.onEdit).toHaveBeenCalledWith({
      kind: 'deleteStep',
      step: 'steps[1]',
      rewire: true,
    });
  });

  it('asks the host for a step after the selected step', () => {
    const onAddStep = vi.fn();
    const { props } = renderCanvas({ selected: 'steps[0]', onAddStep });
    pressKey('n');
    expect(onAddStep).toHaveBeenCalledWith({ mode: 'after' });
    expect(props.onEdit).not.toHaveBeenCalled();
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('asks the host for a step on a dependency edge', () => {
    vi.useFakeTimers();
    const onAddStep = vi.fn();
    renderCanvas({ onAddStep });
    // Only the hovered edge has its buttons.
    expect(screen.queryAllByLabelText('Insert a step here')).toHaveLength(0);
    hoverEdge('depends:steps[0]->steps[1]');
    const buttons = screen.getAllByLabelText('Insert a step here');
    expect(buttons).toHaveLength(1);
    fireEvent.click(buttons[0]!);
    expect(onAddStep).toHaveBeenCalledWith({
      mode: 'insert',
      edgeId: 'depends:steps[0]->steps[1]',
    });
  });

  // Every edit path of the canvas, tried on a canvas that must not edit.
  it.each([
    ['frozen', { frozen: true }],
    ['read-only', { readOnly: true }],
  ] as const)('a %s canvas never calls onEdit', (_, state) => {
    const { props } = renderCanvas({ selected: 'steps[1]', ...state });
    pressKey('Delete');
    pressKey('n');
    expect(screen.queryByRole('combobox')).toBeNull();
    // No Add step button in the canvas.
    expect(screen.queryByText('Add step')).toBeNull();
    expect(screen.queryAllByLabelText('Insert a step here')).toHaveLength(0);
    expect(
      within(nodeWrapper('steps[1]')).queryByLabelText('Delete')
    ).toBeNull();
    expect(screen.queryAllByTestId('ve-canvas-stalk')).toHaveLength(0);
    expect(screen.queryAllByLabelText('Remove dependency')).toHaveLength(0);
    fireEvent.click(edgeElement('depends:steps[0]->steps[1]'));
    pressKey('Backspace');
    expect(props.onEdit).not.toHaveBeenCalled();
  });

  it('opens the list of steps from the stalk of the sink step', () => {
    const onAddStep = vi.fn();
    renderCanvas({ onAddStep });
    // Only publish ends its branch.
    expect(screen.getAllByTestId('ve-canvas-stalk')).toHaveLength(1);
    fireEvent.click(
      within(nodeWrapper('steps[2]')).getByRole('button', {
        name: 'Add a step',
      })
    );
    expect(onAddStep).toHaveBeenCalledWith({
      mode: 'child',
      source: 'steps[2]',
    });
  });

  // parity:canvas-drop-to-add
  it('opens the list of steps for a link dropped on empty space', () => {
    const onAddStep = vi.fn();
    const { props } = renderCanvas({ onAddStep });
    const handle = nodeWrapper('steps[1]').querySelector(
      '[data-flow-handle][data-handle-id="source"]'
    )!;
    pointer(handle, 'pointerdown', 0, 0);
    pointer(window, 'pointermove', -4000, -4000);
    pointer(window, 'pointerup', -4000, -4000);
    expect(onAddStep).toHaveBeenCalledWith({
      mode: 'child',
      source: 'steps[1]',
    });
    expect(props.onEdit).not.toHaveBeenCalled();
  });

  it('removes a dependency from the trash of its edge', () => {
    vi.useFakeTimers();
    const { props } = renderCanvas();
    // A trash on a dependency edge; none on an inferred one.
    hoverEdge('depends:steps[1]->steps[2]');
    const trash = screen.getAllByLabelText('Remove dependency');
    expect(trash).toHaveLength(1);
    fireEvent.click(trash[0]!);
    expect(props.onEdit).toHaveBeenCalledWith({
      kind: 'removeDependency',
      step: 'steps[2]',
      dependsOn: 'steps[1]',
    });
  });

  it('offers the first step on an empty canvas that can edit', () => {
    const onAddStep = vi.fn();
    renderCanvas({ graph: emptyGraph, onAddStep });
    fireEvent.click(screen.getByRole('button', { name: /Add first step/ }));
    expect(onAddStep).toHaveBeenCalledWith({ mode: 'first' });
    expect(screen.queryByText('No steps')).toBeNull();
  });

  it('does nothing when a step is added without a host', () => {
    const { props } = renderCanvas({ graph: emptyGraph });
    fireEvent.click(screen.getByRole('button', { name: /Add first step/ }));
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(props.onEdit).not.toHaveBeenCalled();
  });

  it.each([
    ['frozen', { frozen: true }],
    ['read-only', { readOnly: true }],
  ] as const)('an empty %s canvas only says it has no steps', (_, state) => {
    renderCanvas({ graph: emptyGraph, ...state });
    expect(screen.getByTestId('ve-canvas-empty')).toHaveTextContent('No steps');
    expect(screen.queryByText('Add first step')).toBeNull();
  });

  it('shows no tile when the graph has steps', () => {
    renderCanvas();
    expect(screen.queryByTestId('ve-canvas-empty')).toBeNull();
  });

  it('keeps the browser menu on an edge of a read-only canvas', () => {
    renderCanvas({ readOnly: true });
    const shown = fireEvent.contextMenu(
      edgeElement('depends:steps[0]->steps[1]')
    );
    expect(shown).toBe(true);
    expect(screen.queryByRole('menu')).toBeNull();
  });

  it('opens the edge menu of a canvas that can edit', () => {
    renderCanvas();
    const shown = fireEvent.contextMenu(
      edgeElement('depends:steps[0]->steps[1]')
    );
    expect(shown).toBe(false);
    expect(screen.getByRole('menu')).toBeInTheDocument();
  });

  it('shows the frozen banner and keeps commands', () => {
    const { props } = renderCanvas({ frozen: true, selected: 'steps[0]' });
    expect(screen.getByRole('status')).toHaveTextContent(
      'YAML has errors: the canvas shows the last valid version'
    );
    pressKey('Enter');
    expect(props.onCommand).toHaveBeenCalledWith({
      kind: 'openInspector',
      step: 'steps[0]',
    });
  });

  // The run snapshot has a lock in its bar: one read-only marker only.
  it('drops the read-only chip when the host shows its own marker', () => {
    renderCanvas({ readOnly: true, hideReadOnlyChip: true });
    expect(screen.queryByText('Read-only')).toBeNull();
  });

  // A read-only canvas (a run) shows only what observes: fit, direction,
  // export and filter. Run step stays in the context menu.
  it('keeps open but no run, delete or Add step on a read-only canvas', () => {
    const { props } = renderCanvas({ readOnly: true });
    expect(screen.getByText('Read-only')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add step' })).toBeNull();
    expect(
      screen.getByRole('button', { name: 'Top to bottom' })
    ).toBeInTheDocument();
    fireEvent.pointerEnter(nodeWrapper('steps[2]'));
    const toolbar = within(nodeWrapper('steps[2]'));
    expect(toolbar.queryByLabelText('Run step')).toBeNull();
    expect(toolbar.queryByLabelText('Delete')).toBeNull();
    fireEvent.click(toolbar.getByLabelText('Open step'));
    expect(props.onCommand).toHaveBeenCalledWith({
      kind: 'openInspector',
      step: 'steps[2]',
    });
  });

  it('hides the frozen banner for a host and keeps the frozen style', () => {
    const { container } = renderCanvas({
      frozen: true,
      hideFrozenNotice: true,
    });
    expect(screen.queryByRole('status')).toBeNull();
    const frozen = container.querySelector('[data-frozen]');
    expect(frozen).not.toBeNull();
    expect(frozen).toHaveClass('grayscale');
  });

  // The toolbar content renders inside the context of the canvas.
  function probeActions(overrides: Partial<VisualCanvasProps>) {
    const seen: { current: CanvasActions | null } = { current: null };
    function Probe() {
      seen.current = useCanvasActions();
      return null;
    }
    const view = renderCanvas({ toolbarEnd: <Probe />, ...overrides });
    return { ...view, actions: () => seen.current! };
  }

  it('gives the run gates, the clipboard state and rename to the nodes', () => {
    const gates = {
      canRunStep: true,
      canRetry: () => false,
      canSetStatus: () => false,
      hasSubRuns: () => false,
    };
    const { actions, props } = probeActions({
      stepCommandGates: gates,
      canPaste: true,
    });
    expect(actions().stepCommandGates).toBe(gates);
    expect(actions().clipboard).toEqual({ canPaste: true });
    // Rename opens the name field over the node; the host gets no command.
    act(() => actions().onRename?.('steps[1]'));
    expect(screen.getByRole('textbox', { name: 'Step name' })).toHaveValue(
      'transform'
    );
    expect(props.onCommand).not.toHaveBeenCalled();
  });

  it('gives no paste and no rename to a canvas that cannot edit', () => {
    const { actions } = probeActions({ canPaste: true, readOnly: true });
    expect(actions().clipboard).toEqual({ canPaste: false });
    expect(actions().onRename).toBeUndefined();
    expect(actions().stepCommandGates).toBeUndefined();
  });

  // parity:test-ve-canvas
  it('sends copy, paste and duplicate to the host', () => {
    const { props } = renderCanvas({ selected: 'steps[1]' });
    act(() => keyHandlers().copy?.());
    act(() => keyHandlers().paste?.('- run: echo'));
    act(() => keyHandlers().duplicate?.());
    expect(props.onCommand).toHaveBeenCalledWith({ kind: 'copy' });
    expect(props.onCommand).toHaveBeenCalledWith({
      kind: 'paste',
      text: '- run: echo',
    });
    expect(props.onCommand).toHaveBeenCalledWith({ kind: 'duplicate' });
  });

  it('sends no paste and no duplicate from a canvas that cannot edit', () => {
    const { props } = renderCanvas({ selected: 'steps[1]', readOnly: true });
    act(() => keyHandlers().paste?.('- run: echo'));
    act(() => keyHandlers().duplicate?.());
    act(() => keyHandlers().copy?.());
    expect(props.onCommand).toHaveBeenCalledTimes(1);
    expect(props.onCommand).toHaveBeenCalledWith({ kind: 'copy' });
  });

  it('moves the selection to the neighbour of the arrow', () => {
    const neighbour = vi.mocked(neighbourStep);
    neighbour.mockReturnValueOnce('steps[2]');
    const { props } = renderCanvas({ selected: 'steps[1]' });
    act(() => keyHandlers().navDown?.());
    expect(neighbour).toHaveBeenLastCalledWith(graph, 'steps[1]', 'down', 'LR');
    expect(props.onSelect).toHaveBeenLastCalledWith('steps[2]');
    act(() => keyHandlers().navLeft?.());
    expect(neighbour).toHaveBeenLastCalledWith(graph, 'steps[1]', 'left', 'LR');
    // No neighbour: the selection stays.
    expect(props.onSelect).toHaveBeenCalledTimes(1);
  });

  describe('view handle', () => {
    const sizes = { width: 1000, height: 400 };
    let saved: [PropertyDescriptor | undefined, PropertyDescriptor | undefined];

    beforeEach(() => {
      sizes.width = 1000;
      sizes.height = 400;
      saved = [
        Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth'),
        Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight'),
      ];
      Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
        configurable: true,
        get: () => sizes.width,
      });
      Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
        configurable: true,
        get: () => sizes.height,
      });
    });

    afterEach(() => {
      for (const [index, key] of ['clientWidth', 'clientHeight'].entries()) {
        const descriptor = saved[index];
        if (descriptor) {
          Object.defineProperty(HTMLElement.prototype, key, descriptor);
        } else {
          delete (HTMLElement.prototype as unknown as Record<string, unknown>)[
            key
          ];
        }
      }
    });

    function viewport() {
      const match =
        /translate\((-?[\d.]+)px, (-?[\d.]+)px\) scale\(([\d.]+)\)/.exec(
          screen.getByTestId('flow-layer').style.transform
        );
      if (!match) {
        throw new Error('no viewport');
      }
      return {
        x: Number(match[1]),
        y: Number(match[2]),
        zoom: Number(match[3]),
      };
    }

    function renderWithView(overrides: Partial<VisualCanvasProps> = {}) {
      const viewRef = createRef<CanvasViewHandle>();
      const view = renderCanvas({ viewRef, ...overrides });
      if (!viewRef.current) {
        throw new Error('no view handle');
      }
      return { ...view, view: viewRef.current };
    }

    // parity:canvas-selection
    it('zooms to a level inside the limits and reads it back', () => {
      const { view } = renderWithView();
      act(() => view.zoomTo(1));
      expect(view.getZoom()).toBe(1);
      expect(viewport().zoom).toBe(1);
      act(() => view.zoomTo(1.5));
      expect(view.getZoom()).toBe(1.5);
      act(() => view.zoomTo(50));
      expect(view.getZoom()).toBe(4);
      act(() => view.zoomTo(0.001));
      expect(view.getZoom()).toBe(0.05);
      // A level between two steps lands on the nearest step.
      act(() => view.zoomTo(1.1));
      expect(view.getZoom()).toBe(1);
      act(() => view.zoomTo(1.4));
      expect(view.getZoom()).toBe(1.5);
    });

    // The zoom keys, like the buttons, move one step of the ladder.
    it('moves one ladder step with the zoom keys', () => {
      const { view } = renderWithView();
      act(() => view.zoomTo(1));
      act(() => keyHandlers().zoomIn?.());
      expect(view.getZoom()).toBe(1.25);
      act(() => keyHandlers().zoomIn?.());
      expect(view.getZoom()).toBe(1.5);
      act(() => keyHandlers().zoomOut?.());
      act(() => keyHandlers().zoomOut?.());
      act(() => keyHandlers().zoomOut?.());
      expect(view.getZoom()).toBe(0.75);
      act(() => keyHandlers().zoomReset?.());
      expect(view.getZoom()).toBe(1);
    });

    it('fits on a step of the ladder, and the same each time', () => {
      const { view } = renderWithView();
      const first = viewport();
      expect([0.05, 0.1, 0.25, 0.5, 0.75, 1, 1.25]).toContain(first.zoom);
      act(() => view.zoomTo(3));
      act(() => view.fitView());
      expect(viewport()).toEqual(first);
    });

    it('fits the view again', () => {
      const { view } = renderWithView();
      const fitted = viewport();
      act(() => view.zoomTo(2));
      expect(viewport()).not.toEqual(fitted);
      act(() => view.fitView());
      expect(viewport()).toEqual(fitted);
    });

    // parity:canvas-selection
    it('brings a step to the central zone and keeps the zoom', () => {
      const { view } = renderWithView();
      act(() => view.zoomTo(1.5));
      act(() => view.revealStep('steps[2]'));
      const { zoom } = viewport();
      expect(zoom).toBe(1.5);
      const rect = nodeScreenRect('steps[2]');
      // The free area is 72 px under the top and 48 px above the bottom;
      // the central zone is 60 percent of it.
      const areaTop = 72;
      const areaHeight = sizes.height - 72 - 48;
      expect((rect.left + rect.right) / 2).toBeGreaterThanOrEqual(
        sizes.width * 0.2 - 1e-6
      );
      expect((rect.left + rect.right) / 2).toBeLessThanOrEqual(
        sizes.width * 0.8 + 1e-6
      );
      expect((rect.top + rect.bottom) / 2).toBeGreaterThanOrEqual(
        areaTop + areaHeight * 0.2 - 1e-6
      );
      expect((rect.top + rect.bottom) / 2).toBeLessThanOrEqual(
        areaTop + areaHeight * 0.8 + 1e-6
      );
    });

    // A reveal from outside the canvas (the Runs dock, a chip, a link) moves
    // the view only when the card is not in the central zone.
    it('does not pan to reveal a step in the central zone', () => {
      const { view } = renderWithView();
      act(() => view.zoomTo(1.5));
      act(() => view.revealStep('steps[2]'));
      const before = viewport();
      act(() => view.revealStep('steps[2]'));
      expect(viewport()).toEqual(before);
    });

    it('pans once to reveal a step that is off screen', () => {
      const { view } = renderWithView();
      act(() => view.zoomTo(2));
      const before = viewport();
      const last = graph.nodes[graph.nodes.length - 1]!.key;
      act(() => view.revealStep(last));
      const after = viewport();
      expect(after.zoom).toBe(before.zoom);
      expect(after).not.toEqual(before);
      const rect = nodeScreenRect(last);
      expect(rect.left).toBeGreaterThanOrEqual(0);
      expect(rect.right).toBeLessThanOrEqual(sizes.width);
      act(() => view.revealStep(last));
      expect(viewport()).toEqual(after);
    });

    // Screen box of a node: the viewport transform applied to its position.
    function nodeScreenRect(key: string) {
      const node = nodeWrapper(key);
      const at = /translate\((-?[\d.]+)px, (-?[\d.]+)px\)/.exec(
        node.style.transform
      )!;
      const { x, y, zoom } = viewport();
      const left = x + Number(at[1]) * zoom;
      const top = y + Number(at[2]) * zoom;
      return {
        left,
        top,
        right: left + parseFloat(node.style.width) * zoom,
        bottom: top + parseFloat(node.style.height) * zoom,
      };
    }

    it('fits a graph below the toolbar, inside the side insets', () => {
      sizes.height = 600;
      renderWithView();
      const keys = graph.nodes.map((n) => n.key);
      const rects = keys.map(nodeScreenRect);
      expect(Math.min(...rects.map((r) => r.top))).toBeGreaterThanOrEqual(72);
      expect(Math.min(...rects.map((r) => r.left))).toBeGreaterThanOrEqual(24);
      expect(Math.max(...rects.map((r) => r.right))).toBeLessThanOrEqual(
        sizes.width - 24
      );
      expect(Math.max(...rects.map((r) => r.bottom))).toBeLessThanOrEqual(
        sizes.height
      );
    });

    it('keeps a graph centred in the area under the toolbar', () => {
      sizes.height = 600;
      renderWithView();
      const rects = graph.nodes.map((n) => nodeScreenRect(n.key));
      const left = Math.min(...rects.map((r) => r.left));
      const right = Math.max(...rects.map((r) => r.right));
      const top = Math.min(...rects.map((r) => r.top));
      const bottom = Math.max(...rects.map((r) => r.bottom));
      expect((left + right) / 2).toBeCloseTo(sizes.width / 2, 0);
      // The fit keeps 72 px under the toolbar and 48 px above the controls.
      expect((top + bottom) / 2).toBeCloseTo(
        72 + (sizes.height - 72 - 48) / 2,
        0
      );
    });

    // Every node is inside the canvas area.
    function expectAllInside() {
      for (const node of graph.nodes) {
        const rect = nodeScreenRect(node.key);
        expect(rect.left).toBeGreaterThanOrEqual(0);
        expect(rect.top).toBeGreaterThanOrEqual(0);
        expect(rect.right).toBeLessThanOrEqual(sizes.width);
        expect(rect.bottom).toBeLessThanOrEqual(sizes.height);
      }
    }

    describe('a new layout', () => {
      const without = (keys: string[]) => ({
        ...graph,
        nodes: graph.nodes.filter((node) => !keys.includes(node.key)),
        edges: graph.edges.filter(
          (edge) => !keys.includes(edge.source) && !keys.includes(edge.target)
        ),
      });

      // A direction change gives new positions: the view fits them again,
      // even after the user zoomed.
      it('fits again after a direction change', () => {
        sizes.height = 600;
        const { rerender, props, view } = renderWithView();
        act(() => view.zoomTo(2));
        keyHandlers().zoomIn?.();
        rerender(
          <UserPreferencesProvider>
            <VisualCanvas {...props} direction="TB" />
          </UserPreferencesProvider>
        );
        expect(viewport().zoom).toBeLessThan(2);
        expectAllInside();
      });

      // Undo, redo and discard change the steps of the graph.
      it('fits again when the steps change', () => {
        sizes.height = 600;
        const { rerender, props } = renderWithView({
          graph: without([graph.nodes[0]!.key]),
        });
        rerender(
          <UserPreferencesProvider>
            <VisualCanvas {...props} graph={graph} />
          </UserPreferencesProvider>
        );
        expectAllInside();
      });

      it('fits again after a reveal, not after a pan or zoom of the user', () => {
        sizes.height = 600;
        const smaller = without([graph.nodes[0]!.key]);
        const { rerender, props, view } = renderWithView({ graph: smaller });
        act(() => view.revealStep(smaller.nodes[0]!.key));
        const change = (next: typeof graph) =>
          rerender(
            <UserPreferencesProvider>
              <VisualCanvas {...props} graph={next} />
            </UserPreferencesProvider>
          );
        change(graph);
        expectAllInside();

        act(() => keyHandlers().zoomIn?.());
        const moved = viewport();
        change(smaller);
        expect(viewport()).toEqual(moved);
      });
    });

    describe('the toolbar of a small canvas', () => {
      afterEach(() => {
        sizes.width = 1000;
      });
      // Undo is one of these controls, at the right.
      const toolbarEnd = (
        <>
          <button type="button">Undo</button>
          <button type="button">Workflow</button>
          <button type="button">Save</button>
        </>
      );

      it('keeps its labels on a wide canvas', () => {
        sizes.width = 1200;
        renderWithView({ toolbarEnd });
        expect(screen.getByTestId('ve-toolbar')).not.toHaveAttribute(
          'data-compact'
        );
        expect(
          screen.getByRole('button', { name: 'Undo' })
        ).not.toHaveAttribute('title');
      });

      // The two toolbars of the top have one padding, so one height; a
      // separator that starts the right bar is not drawn.
      it('has the padding of the left toolbar and no leading separator', () => {
        renderWithView({ toolbarEnd });
        const bar = screen.getByTestId('ve-toolbar');
        expect(bar).toHaveClass('p-0.5');
        expect(bar).not.toHaveClass('p-1');
        expect(bar.className).toContain(
          '[&>[aria-hidden=true]:first-child]:hidden'
        );
        const left = screen.getByRole('button', { name: 'Top to bottom' })
          .parentElement as HTMLElement;
        expect(left).toHaveClass('p-0.5');
      });

      // The labels shrink to icons in a narrow bar, except the text of a
      // select (the document selector), and an overflow stays reachable.
      it('keeps the text of a select in the compact bar', () => {
        sizes.width = 1000;
        render(
          <UserPreferencesProvider>
            <CanvasOverlayInsetContext.Provider value={{ right: 600 }}>
              <VisualCanvas
                graph={graph}
                direction="LR"
                selected={null}
                frozen={false}
                readOnly={false}
                catalog={catalogSample}
                onSelect={vi.fn()}
                onEdit={vi.fn()}
                onCommand={vi.fn()}
                toolbarEnd={
                  <button type="button" role="combobox" aria-label="Document">
                    <span>doc (Parent)</span>
                    <svg />
                  </button>
                }
              />
            </CanvasOverlayInsetContext.Provider>
          </UserPreferencesProvider>
        );
        const bar = screen.getByTestId('ve-toolbar');
        expect(bar.className).toContain(
          '[&_button:has(>svg):not([role=combobox])]:text-[0px]'
        );
        expect(bar.className).not.toContain('[&_button:has(>svg)]:text-[0px]');
        expect(bar.className).toContain('justify-end-safe');
      });

      // A panel over the canvas leaves a strip: one row, icons, tooltips.
      it('goes to one row of icons when the free width is small', () => {
        sizes.width = 1000;
        render(
          <UserPreferencesProvider>
            <CanvasOverlayInsetContext.Provider value={{ right: 600 }}>
              <VisualCanvas
                graph={graph}
                direction="LR"
                selected={null}
                frozen={false}
                readOnly={false}
                catalog={catalogSample}
                onSelect={vi.fn()}
                onEdit={vi.fn()}
                onCommand={vi.fn()}
                toolbarEnd={toolbarEnd}
              />
            </CanvasOverlayInsetContext.Provider>
          </UserPreferencesProvider>
        );
        const bar = screen.getByTestId('ve-toolbar');
        expect(bar).toHaveAttribute('data-compact', 'true');
        expect(bar.className).toContain('flex-nowrap');
        expect(bar.className).not.toContain('flex-wrap');
        // Clear of the panel, and a name as tooltip on each button.
        expect(bar.parentElement?.style.right).toBe('608px');
        expect(screen.getByRole('button', { name: 'Undo' })).toHaveAttribute(
          'title',
          'Undo'
        );
        expect(screen.getByRole('button', { name: 'Save' })).toHaveAttribute(
          'title',
          'Save'
        );
        // The overview needs room: not drawn in a strip of 400 px.
        expect(screen.queryByTestId('flow-minimap')).toBeNull();
      });

      // The fit keeps its content clear of the panel.
      it('fits the graph in the part that the panel leaves free', () => {
        sizes.width = 1000;
        sizes.height = 600;
        render(
          <UserPreferencesProvider>
            <CanvasOverlayInsetContext.Provider value={{ right: 500 }}>
              <VisualCanvas
                graph={graph}
                direction="TB"
                selected={null}
                frozen={false}
                readOnly={false}
                catalog={catalogSample}
                onSelect={vi.fn()}
                onEdit={vi.fn()}
                onCommand={vi.fn()}
              />
            </CanvasOverlayInsetContext.Provider>
          </UserPreferencesProvider>
        );
        const rects = graph.nodes.map((n) => nodeScreenRect(n.key));
        expect(Math.max(...rects.map((r) => r.right))).toBeLessThanOrEqual(
          1000 - 500 - 24
        );
      });
    });

    // A reveal never changes the zoom, even a tiny one.
    it('keeps a tiny zoom when it reveals a step', () => {
      const { view } = renderWithView();
      act(() => view.zoomTo(0.1));
      act(() => view.revealStep('steps[2]'));
      expect(viewport().zoom).toBe(0.1);
    });

    describe('an agent graph', () => {
      afterEach(() => {
        sizes.width = 1000;
        sizes.height = 400;
      });
      const tools = (count: number) =>
        Array.from({ length: count }, (_, i) =>
          makeStep({ key: `steps[${i}]`, explicitName: `tool ${i + 1}` })
        );
      const agentGraph = (count: number) => {
        const model = makeModel(tools(count), {
          dagType: 'agent',
          text: 'type: agent\nllm:\n  provider: p\n  model: m\n',
        });
        return buildCanvasGraph({
          model,
          names: makeNames(model),
          issues: { byStep: {}, dag: [] },
        });
      };
      const columnsOf = () => {
        const xs = new Set(
          Array.from(
            document.querySelectorAll<HTMLElement>('[data-shape="tool"]')
          ).map(
            (card) =>
              /translate\((-?[\d.]+)px/.exec(
                card.closest<HTMLElement>('[data-flow-node]')!.style.transform
              )![1]
          )
        );
        return xs.size;
      };

      it('fits 100 tools at a readable zoom and shows the top', () => {
        sizes.height = 600;
        renderWithView({ graph: agentGraph(100), direction: 'TB' });
        // The whole grid would need about 0.3 at this size.
        expect(viewport().zoom).toBeGreaterThanOrEqual(0.5);
        expect(viewport().y).toBe(72);
      });

      // The top of the hub is below the 56 px toolbar, after the first fit
      // and after the fit button.
      it('fits the hub below the toolbar', () => {
        sizes.height = 600;
        renderWithView({ graph: agentGraph(100), direction: 'TB' });
        const hubTop = () => nodeScreenRect('agent').top;
        expect(hubTop()).toBeGreaterThanOrEqual(56 + 16);
        fireEvent.wheel(screen.getByTestId('ve-canvas'), { deltaY: 300 });
        fireEvent.click(screen.getByRole('button', { name: 'Fit view' }));
        expect(hubTop()).toBeGreaterThanOrEqual(56 + 16);
      });

      // The filter brings a match into view, and Enter goes to the next.
      it('pans to the first match of the filter and steps with Enter', () => {
        sizes.height = 600;
        renderWithView({ graph: agentGraph(100), direction: 'TB' });
        const inside = (key: string) => {
          const rect = nodeScreenRect(key);
          return (
            rect.bottom > 0 &&
            rect.top < sizes.height &&
            rect.right > 0 &&
            rect.left < sizes.width
          );
        };
        expect(inside('steps[69]')).toBe(false);
        const box = screen.getByRole('searchbox', {
          name: 'Filter tools by name',
        });
        fireEvent.change(box, { target: { value: 'tool 70' } });
        expect(inside('steps[69]')).toBe(true);

        fireEvent.change(box, { target: { value: 'tool 7' } });
        expect(inside('steps[6]')).toBe(true);
        fireEvent.keyDown(box, { key: 'Enter' });
        expect(inside('steps[69]')).toBe(true);
        fireEvent.keyDown(box, { key: 'Enter', shiftKey: true });
        expect(inside('steps[6]')).toBe(true);
      });

      it('fits a short agent below the toolbar too', () => {
        sizes.height = 900;
        renderWithView({ graph: agentGraph(3), direction: 'TB' });
        expect(nodeScreenRect('agent').top).toBeGreaterThanOrEqual(72);
      });

      it('chooses the columns from the width of the canvas', () => {
        sizes.width = 1000;
        const narrow = renderWithView({
          graph: agentGraph(30),
          direction: 'TB',
        });
        expect(columnsOf()).toBe(agentColumns(1000));
        narrow.unmount();
        sizes.width = 1900;
        renderWithView({ graph: agentGraph(30), direction: 'TB' });
        expect(columnsOf()).toBe(agentColumns(1900));
        expect(agentColumns(1900)).toBeGreaterThan(agentColumns(1000));
      });

      it('moves the selection through the grid with the arrow keys', () => {
        sizes.width = 1000;
        const { props } = renderWithView({
          graph: agentGraph(10),
          direction: 'TB',
          selected: 'steps[1]',
        });
        act(() => keyHandlers().navRight?.());
        expect(props.onSelect).toHaveBeenLastCalledWith('steps[2]');
        act(() => keyHandlers().navDown?.());
        // 4 columns at 1000 px.
        expect(props.onSelect).toHaveBeenLastCalledWith('steps[5]');
        act(() => keyHandlers().navUp?.());
        expect(props.onSelect).toHaveBeenLastCalledWith('agent');
      });

      it('has a filter box on the toolbar for an agent only', () => {
        const agent = renderWithView({ graph: agentGraph(3) });
        expect(screen.getByRole('searchbox')).toBeInTheDocument();
        agent.unmount();
        renderWithView();
        expect(screen.queryByRole('searchbox')).toBeNull();
      });
    });

    // The wheel moves a graph of any kind; Ctrl or Cmd zooms.
    it('pans a classic graph with the wheel and zooms with Ctrl', async () => {
      renderWithView();
      const flow = screen.getByTestId('flow');
      // The wheel events of one frame change the view at the next frame.
      const frame = () =>
        act(async () => {
          await new Promise<void>((resolve) =>
            requestAnimationFrame(() => resolve())
          );
        });
      const before = viewport();
      fireEvent.wheel(flow, { deltaY: 120 });
      await frame();
      const panned = viewport();
      expect(panned.zoom).toBe(before.zoom);
      expect(panned.y).not.toBe(before.y);
      fireEvent.wheel(flow, { deltaY: -120, ctrlKey: true });
      await frame();
      expect(viewport().zoom).toBeGreaterThan(before.zoom);
    });

    describe('a long graph', () => {
      let observers: ((entries: unknown[]) => void)[] = [];
      beforeEach(() => {
        observers = [];
        vi.stubGlobal(
          'ResizeObserver',
          class {
            constructor(callback: (entries: unknown[]) => void) {
              observers.push(callback);
            }
            observe() {}
            unobserve() {}
            disconnect() {}
          }
        );
      });
      afterEach(() => {
        vi.unstubAllGlobals();
        vi.useRealTimers();
        sizes.width = 1000;
        sizes.height = 400;
      });
      const longGraph = (count: number) => {
        const model = makeModel(
          Array.from({ length: count }, (_, i) =>
            makeStep({
              key: `steps[${i}]`,
              explicitName: `s${i}`,
              depends: i === 0 ? [] : [`s${i - 1}`],
            })
          )
        );
        return buildCanvasGraph({
          model,
          names: makeNames(model),
          issues: { byStep: {}, dag: [] },
        });
      };
      // The size changes as a layout change does: the observers report it.
      const resize = (width: number, height: number) => {
        sizes.width = width;
        sizes.height = height;
        act(() => {
          observers.forEach((callback) =>
            callback([{ contentRect: { width, height } }])
          );
        });
      };
      const inFreeArea = (key: string) => {
        const rect = nodeScreenRect(key);
        expect(rect.top).toBeGreaterThanOrEqual(72);
        expect(rect.bottom).toBeLessThanOrEqual(sizes.height - 48);
        expect(rect.left).toBeGreaterThanOrEqual(24);
        expect(rect.right).toBeLessThanOrEqual(sizes.width - 24);
      };

      // The keys move the selection: the view follows it, at the same zoom.
      it('brings the step of an arrow key into the free area', () => {
        sizes.height = 600;
        const long = longGraph(40);
        const neighbour = vi.mocked(neighbourStep);
        neighbour.mockReturnValueOnce('steps[30]');
        renderWithView({ graph: long, direction: 'TB', selected: 'steps[0]' });
        const zoom = viewport().zoom;
        act(() => keyHandlers().navDown?.());
        expect(viewport().zoom).toBe(zoom);
        inFreeArea('steps[30]');
      });

      it('does not move the view for a step that is already visible', () => {
        sizes.height = 600;
        const neighbour = vi.mocked(neighbourStep);
        neighbour.mockReturnValueOnce('steps[1]');
        renderWithView({
          graph: longGraph(40),
          direction: 'TB',
          selected: 'steps[0]',
        });
        const before = viewport();
        act(() => keyHandlers().navDown?.());
        expect(viewport()).toEqual(before);
      });

      // The dock opens when a step is selected: the canvas gets shorter and
      // the selected node must stay in the part that shows.
      it('keeps the selected step in view when the canvas gets shorter', () => {
        vi.useFakeTimers();
        sizes.height = 900;
        renderWithView({
          graph: longGraph(40),
          direction: 'TB',
          selected: 'steps[8]',
        });
        // The first report of an observer is the initial size.
        resize(1000, 900);
        resize(1000, 450);
        act(() => {
          vi.advanceTimersByTime(300);
        });
        inFreeArea('steps[8]');
      });

      // A reveal asked while the canvas is hidden waits for the size.
      it('applies a reveal that came before the canvas had a size', () => {
        sizes.width = 0;
        sizes.height = 0;
        const { view } = renderWithView({
          graph: longGraph(40),
          direction: 'TB',
        });
        act(() => view.revealStep('steps[25]'));
        resize(1000, 600);
        inFreeArea('steps[25]');
      });

      it('applies a reveal again to the final size of the canvas', () => {
        sizes.height = 900;
        const { view } = renderWithView({
          graph: longGraph(40),
          direction: 'TB',
        });
        act(() => view.revealStep('steps[25]'));
        resize(1000, 450);
        inFreeArea('steps[25]');
      });

      it('stops waiting for a size after a pan of the user', () => {
        sizes.height = 900;
        const { view } = renderWithView({
          graph: longGraph(40),
          direction: 'TB',
        });
        act(() => view.revealStep('steps[25]'));
        fireEvent.wheel(screen.getByTestId('flow'), { deltaY: 40 });
        const panned = viewport();
        resize(1000, 450);
        expect(viewport()).toEqual(panned);
      });
    });

    it('zooms in, out and back to 100% with the zoom keys', () => {
      const { view } = renderWithView();
      act(() => keyHandlers().zoomReset?.());
      expect(view.getZoom()).toBe(1);
      act(() => keyHandlers().zoomIn?.());
      expect(view.getZoom()).toBeGreaterThan(1);
      act(() => keyHandlers().zoomReset?.());
      act(() => keyHandlers().zoomOut?.());
      expect(view.getZoom()).toBeLessThan(1);
    });

    it('does not move for an unknown step', () => {
      const { view } = renderWithView();
      const before = viewport();
      act(() => view.revealStep('steps[99]'));
      expect(viewport()).toEqual(before);
    });
  });
});

describe('the top-left toolbar and the display options', () => {
  afterEach(() => {
    window.localStorage.clear();
  });

  // The zoom buttons and the export button sit beside the direction arrows;
  // the right toolbar and the old corners no longer have them.
  it('has the zoom, export and display buttons beside the arrows', () => {
    renderCanvas();
    const left = screen.getByRole('button', { name: 'Top to bottom' })
      .parentElement as HTMLElement;
    for (const name of [
      'Left to right',
      'Zoom in',
      'Zoom out',
      'Fit view',
      'Export graph',
    ]) {
      expect(within(left).getByRole('button', { name })).toBeInTheDocument();
    }
    expect(
      within(left).getByRole('button', { name: 'Simplified view' })
    ).toHaveAttribute('aria-pressed', 'false');
    expect(within(left).getByTestId('flow-zoom-reset')).toBeInTheDocument();
    const right = screen.queryByTestId('ve-toolbar');
    if (right) {
      expect(
        within(right).queryByRole('button', { name: 'Export graph' })
      ).toBeNull();
    }
  });

  it('shows only the icons of the steps when Simplified view is pressed', async () => {
    renderCanvas();
    const first = graph.nodes[0]!;
    expect(within(nodeCard(first.key)).getByText(first.label)).toBeVisible();
    const before = nodeWrapper(first.key).style.width;
    await userEvent.click(
      screen.getByRole('button', { name: 'Simplified view' })
    );
    expect(within(nodeCard(first.key)).queryByText(first.label)).toBeNull();
    expect(nodeCard(first.key)).toHaveAttribute('aria-label', first.label);
    expect(nodeWrapper(first.key).style.width).toBe('56px');
    expect(before).toBe('240px');
    expect(window.localStorage.getItem('dagu.canvas.details')).toBe('0');
  });
});

describe('VisualCanvas launchers', () => {
  const launchers = launcherStates({
    scheduleCount: 0,
    webhookCount: 1,
    webhooksAvailable: true,
  });

  it('draws a node for each launcher next to the step cards', () => {
    renderCanvas({ launchers });
    expect(screen.getByTestId('ve-launcher-manual')).toHaveAttribute(
      'data-status',
      'active'
    );
    // Not set up: dashed.
    expect(screen.getByTestId('ve-launcher-cron')).toHaveAttribute(
      'data-status',
      'dashed'
    );
    expect(screen.getByTestId('ve-launcher-webhook')).toHaveAttribute(
      'data-status',
      'active'
    );
    for (const node of graph.nodes) {
      expect(nodeCard(node.key)).toBeInTheDocument();
    }
  });

  it('draws no Webhook node when the instance has no webhooks', () => {
    renderCanvas({
      launchers: launcherStates({
        scheduleCount: 1,
        webhookCount: null,
        webhooksAvailable: false,
      }),
    });
    expect(screen.queryByTestId('ve-launcher-webhook')).toBeNull();
    expect(screen.getByTestId('ve-launcher-cron')).toHaveAttribute(
      'data-status',
      'active'
    );
  });

  it('draws nothing without launchers', () => {
    renderCanvas();
    expect(screen.queryByTestId('ve-launcher-manual')).toBeNull();
  });

  it('opens a launcher on click and selects no step', () => {
    const onLauncher = vi.fn();
    const { props } = renderCanvas({ launchers, onLauncher });
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-cron')).getByRole('button')
    );
    expect(onLauncher).toHaveBeenCalledWith('cron');
    expect(props.onSelect).not.toHaveBeenCalled();
  });

  it('opens no inspector on a double click of a launcher', () => {
    const { props } = renderCanvas({ launchers, onLauncher: vi.fn() });
    fireEvent.doubleClick(screen.getByTestId('ve-launcher-manual'));
    expect(props.onCommand).not.toHaveBeenCalled();
  });

  // The owner asked to see how many cron expressions are set up.
  describe('count of cron expressions', () => {
    const withSchedules = (scheduleCount: number) =>
      launcherStates({
        scheduleCount,
        webhookCount: 0,
        webhooksAvailable: true,
      });
    const cronButton = () =>
      within(screen.getByTestId('ve-launcher-cron')).getByRole('button');

    it('shows the number as a badge and names it on the node', () => {
      renderCanvas({ launchers: withSchedules(2) });
      expect(screen.getByTestId('ve-launcher-count')).toHaveTextContent('2');
      expect(cronButton()).toHaveAttribute(
        'aria-label',
        'Cron, 2 schedules: Scheduled'
      );
      expect(cronButton()).toHaveAttribute(
        'title',
        'Cron, 2 schedules: Scheduled'
      );
    });

    it('uses the singular for one schedule', () => {
      renderCanvas({ launchers: withSchedules(1) });
      expect(screen.getByTestId('ve-launcher-count')).toHaveTextContent('1');
      expect(cronButton()).toHaveAttribute(
        'aria-label',
        'Cron, 1 schedule: Scheduled'
      );
    });

    it('hides the badge when there is no schedule', () => {
      renderCanvas({ launchers: withSchedules(0) });
      expect(screen.queryByTestId('ve-launcher-count')).toBeNull();
      expect(cronButton()).toHaveAttribute('aria-label', 'Cron: Not set up');
    });

    it('keeps the badge in the simplified view', () => {
      window.localStorage.setItem(CANVAS_DETAILS_KEY, '0');
      try {
        renderCanvas({ launchers: withSchedules(3) });
        expect(screen.getByTestId('ve-launcher-count')).toHaveTextContent('3');
        expect(cronButton()).toHaveAttribute(
          'aria-label',
          'Cron, 3 schedules: Scheduled'
        );
      } finally {
        window.localStorage.removeItem(CANVAS_DETAILS_KEY);
      }
    });

    // The owner asked for the name alone: no second line.
    it('shows only the name on Cron, Manual and Webhook, without a subtitle', () => {
      renderCanvas({ launchers: withSchedules(2) });
      const button = (id: string) =>
        within(screen.getByTestId(`ve-launcher-${id}`)).getByRole('button');
      expect(button('manual')).toHaveTextContent(/^Manual$/);
      expect(button('webhook')).toHaveTextContent(/^Webhook$/);
      // The count badge is the only other text of the Cron card.
      expect(button('cron')).toHaveTextContent(/^Cron2$/);
      for (const id of ['manual', 'cron', 'webhook']) {
        expect(button(id)).not.toHaveTextContent(
          /Always on|Not set up|Scheduled|Enabled/
        );
      }
      // The state stays in the accessible name and the tooltip.
      expect(button('manual')).toHaveAttribute(
        'aria-label',
        'Manual: Always on'
      );
      expect(button('manual')).toHaveAttribute('title', 'Manual: Always on');
      expect(button('webhook')).toHaveAttribute(
        'aria-label',
        'Webhook: Not set up'
      );
    });

    it('draws the cron count with the shared count badge', () => {
      renderCanvas({ launchers: withSchedules(2) });
      expect(screen.getByTestId('ve-launcher-count')).toHaveClass(
        'h-5',
        'min-w-5',
        'rounded-full',
        'bg-primary',
        'absolute'
      );
    });

    it('shows no badge on Manual and Webhook', () => {
      renderCanvas({ launchers: withSchedules(2) });
      expect(
        within(screen.getByTestId('ve-launcher-manual')).queryByTestId(
          've-launcher-count'
        )
      ).toBeNull();
      expect(
        within(screen.getByTestId('ve-launcher-webhook')).queryByTestId(
          've-launcher-count'
        )
      ).toBeNull();
    });
  });

  // The owner asked that a dashed node and its edges stay dashed when the node
  // is focused or open, with the same dashes for the border and the edges.
  describe('dashes of a launcher that is not set up', () => {
    const outline = () =>
      within(screen.getByTestId('ve-launcher-cron')).getByTestId(
        've-launcher-outline'
      );
    const rect = () => outline().querySelector('rect')!;

    it('draws the border as dashes of the same pattern as the edges', () => {
      renderCanvas({ launchers });
      expect(rect().getAttribute('stroke-dasharray')).toBe('6 4');
      // The edges of one launcher share their line to the trunk: only the
      // first of them draws it.
      const edges = screen
        .getAllByTestId('ve-launcher-edge')
        .filter((edge) => edge.getAttribute('data-status') === 'dashed')
        .filter((edge) => edge.querySelector('path[data-flow-edge-path]'));
      expect(edges.length).toBeGreaterThan(0);
      for (const edge of edges) {
        expect(
          edge.querySelector<SVGPathElement>('path[data-flow-edge-path]')!.style
            .strokeDasharray
        ).toBe('6 4');
      }
    });

    it('keeps the dashes of the border and of the edges when the node is open', () => {
      renderCanvas({ launchers, activeLauncher: 'cron' });
      expect(rect().getAttribute('stroke-dasharray')).toBe('6 4');
      expect(rect().getAttribute('class')).toContain('stroke-primary');
      // No solid ring over the dashes.
      expect(
        within(screen.getByTestId('ve-launcher-cron')).getByRole('button')
      ).not.toHaveClass('ring-2');
      const lit = screen
        .getAllByTestId('ve-launcher-edge')
        .filter((edge) => edge.getAttribute('data-active') === 'true')
        .filter((edge) => edge.querySelector('path[data-flow-edge-path]'));
      expect(lit.length).toBeGreaterThan(0);
      for (const edge of lit) {
        const path = edge.querySelector<SVGPathElement>(
          'path[data-flow-edge-path]'
        )!;
        expect(path.style.strokeDasharray).toBe('6 4');
        expect(path.style.stroke).toBe('var(--primary)');
      }
    });

    it('lights the dashes on hover and focus, never a solid ring', () => {
      renderCanvas({ launchers });
      expect(rect().getAttribute('class')).toContain(
        'group-hover:stroke-primary'
      );
      expect(rect().getAttribute('class')).toContain(
        'group-focus-visible:stroke-primary'
      );
      const button = within(screen.getByTestId('ve-launcher-cron')).getByRole(
        'button'
      );
      expect(button).toHaveClass('group');
      expect(button.className).not.toContain('focus-visible:ring');
    });

    it('draws a set up launcher solid', () => {
      renderCanvas({ launchers });
      expect(
        within(screen.getByTestId('ve-launcher-webhook')).queryByTestId(
          've-launcher-outline'
        )
      ).toBeNull();
    });
  });

  it('draws no launcher on an agent graph', () => {
    const agent = {
      nodes: [
        {
          key: 'agent',
          label: 'agent',
          kind: 'agent' as const,
          lane: 'main' as const,
          issues: { errors: 0, warnings: 0 },
        },
      ],
      edges: [],
      editableEdges: false,
    };
    renderCanvas({ graph: agent, launchers });
    expect(screen.queryByTestId('ve-launcher-manual')).toBeNull();
  });
});

describe('VisualCanvas ghost step', () => {
  const ghostKey = 'ghost:new';
  const look = {
    title: 'HTTP request',
    action: 'http.request',
    kind: 'action' as const,
  };

  it('draws no ghost without the prop', () => {
    renderCanvas();
    expect(screen.queryByTestId('ve-canvas-ghost')).toBeNull();
  });

  it('draws a generic dashed card with a dashed edge from the step', () => {
    renderCanvas({
      selected: 'steps[0]',
      ghost: { target: { mode: 'after' }, look: null },
    });
    const ghost = screen.getByTestId('ve-canvas-ghost');
    expect(ghost).toHaveAttribute('data-ghost', 'new');
    expect(ghost).toHaveClass('border-dashed');
    expect(ghost).toHaveTextContent('New step');
    expect(screen.getAllByTestId('ve-canvas-ghost-edge')).toHaveLength(1);
    // The real steps stay.
    for (const node of graph.nodes) {
      expect(nodeCard(node.key)).toBeInTheDocument();
    }
  });

  it('shows the choice of the list and updates it in place', () => {
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
      ghost: { target: { mode: 'after' }, look: null },
    };
    const ui = (p: VisualCanvasProps) => (
      <UserPreferencesProvider>
        <VisualCanvas {...p} />
      </UserPreferencesProvider>
    );
    const { rerender } = render(ui(props));
    const before = screen
      .getByTestId('ve-canvas-ghost')
      .closest<HTMLElement>('[data-flow-node]')!.style.transform;
    rerender(ui({ ...props, ghost: { target: { mode: 'after' }, look } }));
    const ghost = screen.getByTestId('ve-canvas-ghost');
    expect(ghost).toHaveAttribute('data-ghost', 'http.request');
    expect(ghost).toHaveTextContent('HTTP request');
    // A hover does not move the nodes.
    expect(
      ghost.closest<HTMLElement>('[data-flow-node]')!.style.transform
    ).toBe(before);
    rerender(ui({ ...props, ghost: null }));
    expect(screen.queryByTestId('ve-canvas-ghost')).toBeNull();
  });

  it('hides the link that the ghost replaces on an insert', () => {
    const edgeId = 'depends:steps[0]->steps[1]';
    expect(graph.edges.some((edge) => edge.id === edgeId)).toBe(true);
    renderCanvas({
      ghost: { target: { mode: 'insert', edgeId }, look },
    });
    // Source -> ghost and ghost -> target replace the edge.
    expect(screen.getAllByTestId('ve-canvas-ghost-edge')).toHaveLength(2);
  });

  it('is not selectable and has no menu or inspector', () => {
    const { props } = renderCanvas({
      ghost: { target: { mode: 'after' }, look },
    });
    const ghost = screen.getByTestId('ve-canvas-ghost');
    fireEvent.click(ghost);
    fireEvent.doubleClick(ghost);
    fireEvent.contextMenu(ghost);
    expect(props.onSelect).not.toHaveBeenCalled();
    expect(props.onCommand).not.toHaveBeenCalled();
    expect(screen.queryByRole('menu')).toBeNull();
    expect(ghost).toHaveAttribute('aria-hidden', 'true');
    expect(ghostKey).toBe('ghost:new');
  });

  it('shows only the icon in the simplified mode', async () => {
    window.localStorage.setItem(CANVAS_DETAILS_KEY, '0');
    renderCanvas({ ghost: { target: { mode: 'after' }, look } });
    const ghost = screen.getByTestId('ve-canvas-ghost');
    expect(ghost).toHaveClass('w-14');
    expect(ghost).not.toHaveTextContent('HTTP request');
    window.localStorage.removeItem(CANVAS_DETAILS_KEY);
  });

  it('draws no ghost on a read-only or frozen canvas', () => {
    renderCanvas({
      readOnly: true,
      ghost: { target: { mode: 'after' }, look },
    });
    expect(screen.queryByTestId('ve-canvas-ghost')).toBeNull();
  });
});
