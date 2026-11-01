// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import {
  BaseEdge,
  Background,
  Controls,
  Flow,
  FlowProvider,
  getCurvePath,
  Handle,
  MiniMap,
  Panel,
  Position,
  type EdgeProps,
  type FlowEdge,
  type FlowNode,
  type NodeProps,
} from '..';

type TestNode = FlowNode<{ label: string }, 'box'>;
type TestEdge = FlowEdge<Record<string, unknown>, 'line'>;

function Box({ data, isConnectable }: NodeProps<TestNode>) {
  return (
    <div className="relative h-16 w-60 border">
      <Handle
        id="target"
        type="target"
        position={Position.Left}
        isConnectable={isConnectable}
      />
      {data.label}
      <Handle
        id="source"
        type="source"
        position={Position.Right}
        isConnectable={isConnectable}
      />
    </div>
  );
}

function Line(props: EdgeProps<TestEdge>) {
  const [path] = getCurvePath(props);
  return (
    <g data-testid={`edge-${props.id}`}>
      <BaseEdge id={props.id} path={path} markerEnd={props.markerEnd} />
    </g>
  );
}

const nodes: TestNode[] = [
  {
    id: 'a',
    type: 'box',
    position: { x: 0, y: 0 },
    width: 240,
    height: 64,
    data: { label: 'A' },
  },
  {
    id: 'b',
    type: 'box',
    position: { x: 300, y: 0 },
    width: 240,
    height: 64,
    data: { label: 'B' },
  },
];
const edges: TestEdge[] = [
  {
    id: 'a-b',
    source: 'a',
    target: 'b',
    sourceHandle: 'source',
    targetHandle: 'target',
    type: 'line',
    data: {},
  },
];

type Handlers = Partial<Parameters<typeof Flow<TestNode, TestEdge>>[0]>;

function renderFlow(handlers: Handlers = {}) {
  return render(
    <UserPreferencesProvider>
      <FlowProvider>
        <Flow<TestNode, TestEdge>
          nodes={nodes}
          edges={edges}
          nodeTypes={{ box: Box }}
          edgeTypes={{ line: Line }}
          {...handlers}
        >
          <Background />
          <Panel position="top-left">
            <Controls />
          </Panel>
          <MiniMap />
        </Flow>
      </FlowProvider>
    </UserPreferencesProvider>
  );
}

function layerTransform(): string {
  return screen.getByTestId('flow-layer').style.transform;
}

function nodeBox(id: string): HTMLElement {
  const element = document.querySelector<HTMLElement>(
    `[data-flow-node="${id}"]`
  );
  if (!element) {
    throw new Error(`no node ${id}`);
  }
  return element;
}

function handleOf(id: string, handleId: string): HTMLElement {
  const element = nodeBox(id).querySelector<HTMLElement>(
    `[data-flow-handle][data-handle-id="${handleId}"]`
  );
  if (!element) {
    throw new Error(`no handle ${handleId} on ${id}`);
  }
  return element;
}

function pointer(
  target: Element | Window,
  type: 'pointerdown' | 'pointermove' | 'pointerup' | 'pointercancel',
  x: number,
  y: number
) {
  // Mouse events carry the coordinates in every jsdom version.
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

// The wheel events of one frame change the view at the next frame.
const frame = () =>
  act(async () => {
    await new Promise<void>((resolve) =>
      requestAnimationFrame(() => resolve())
    );
  });

describe('Flow', () => {
  it('places each node at its position and draws the edge', () => {
    renderFlow();
    expect(nodeBox('a').style.transform).toBe('translate(0px, 0px)');
    expect(nodeBox('b').style.transform).toBe('translate(300px, 0px)');
    const path = screen.getByTestId('edge-a-b').querySelector('path');
    expect(path?.getAttribute('d')).toMatch(/^M/);
    expect(path?.getAttribute('marker-end')).toMatch(/^url\(#/);
  });

  // The tip of the head is on the end of the path, which is the handle of
  // the target, and the head takes the stroke of its edge.
  it('puts the tip of the arrowhead on the end of the edge', () => {
    const { container } = renderFlow();
    const marker = container.querySelector('marker');
    expect(marker?.getAttribute('orient')).toBe('auto');
    const head = marker?.querySelector('path');
    // The head is drawn in a 10 unit box: its tip is at (10, 5).
    expect(head?.getAttribute('d')).toBe('M 0 0 L 10 5 L 0 10 z');
    expect(marker?.getAttribute('refX')).toBe('10');
    expect(marker?.getAttribute('refY')).toBe('5');
    expect(head?.getAttribute('fill')).toBe('var(--muted-foreground)');
  });

  // A pan or a zoom moves the layer and the dots by their style: no node and
  // no edge draws again.
  it('moves the view without drawing a node or an edge again', () => {
    const drawn = { node: 0, edge: 0 };
    const Counted = (props: NodeProps<TestNode>) => {
      drawn.node += 1;
      return <Box {...props} />;
    };
    const Link = (props: EdgeProps<TestEdge>) => {
      drawn.edge += 1;
      return <Line {...props} />;
    };
    renderFlow({
      nodeTypes: { box: Counted },
      edgeTypes: { line: Link },
    });
    const root = screen.getByTestId('flow');
    // The first moves of the gesture settle the cursor state.
    pointer(root, 'pointerdown', 100, 100);
    pointer(window, 'pointermove', 120, 110);
    const before = { ...drawn };
    pointer(window, 'pointermove', 140, 130);
    pointer(window, 'pointermove', 160, 150);
    expect(layerTransform()).toBe('translate(60px, 50px) scale(1)');
    // The dots move by less than one tile of 16 px.
    expect(screen.getByTestId('flow-background').style.transform).toBe(
      'translate(12px, 2px)'
    );
    expect(drawn).toEqual(before);
    pointer(window, 'pointerup', 160, 150);
  });

  it('clips the frame, so that a focus cannot scroll it', () => {
    renderFlow();
    expect(screen.getByTestId('flow')).toHaveClass('overflow-clip');
    expect(screen.getByTestId('flow')).not.toHaveClass('overflow-hidden');
  });

  it('draws the edge from the handle of the node, not from a guess', () => {
    renderFlow();
    const d = screen
      .getByTestId('edge-a-b')
      .querySelector('path')!
      .getAttribute('d')!;
    // jsdom has no layout, so each handle sits at the corner of its node.
    expect(d.startsWith('M0,0 ')).toBe(true);
    // It ends on the outer edge of the dot of the target (4 px), not on its centre.
    expect(d.endsWith(' L296,0')).toBe(true);
  });

  it('calls the click handlers of the node, the edge and the pane', () => {
    const onNodeClick = vi.fn();
    const onNodeDoubleClick = vi.fn();
    const onEdgeClick = vi.fn();
    const onPaneClick = vi.fn();
    renderFlow({ onNodeClick, onNodeDoubleClick, onEdgeClick, onPaneClick });

    fireEvent.click(nodeBox('b'));
    expect(onNodeClick).toHaveBeenCalledWith('b', expect.anything());
    fireEvent.doubleClick(nodeBox('a'));
    expect(onNodeDoubleClick).toHaveBeenCalledWith('a');
    fireEvent.click(document.querySelector('[data-flow-edge="a-b"]')!);
    expect(onEdgeClick).toHaveBeenCalledWith('a-b');
    expect(onPaneClick).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('flow'));
    expect(onPaneClick).toHaveBeenCalledTimes(1);
  });

  describe('pan and zoom', () => {
    it('pans with a drag on the pane, and the drag is not a click', () => {
      const onUserMove = vi.fn();
      const onPaneClick = vi.fn();
      renderFlow({ onUserMove, onPaneClick });
      const root = screen.getByTestId('flow');

      pointer(root, 'pointerdown', 100, 100);
      pointer(window, 'pointermove', 140, 130);
      pointer(window, 'pointerup', 140, 130);
      expect(layerTransform()).toBe('translate(40px, 30px) scale(1)');
      expect(onUserMove).toHaveBeenCalledTimes(1);

      fireEvent.click(root);
      expect(onPaneClick).not.toHaveBeenCalled();
      // The next click is a click again.
      fireEvent.click(root);
      expect(onPaneClick).toHaveBeenCalledTimes(1);
    });

    it('does not pan from a small move', () => {
      renderFlow();
      const root = screen.getByTestId('flow');
      pointer(root, 'pointerdown', 100, 100);
      pointer(window, 'pointermove', 101, 101);
      pointer(window, 'pointerup', 101, 101);
      expect(layerTransform()).toBe('translate(0px, 0px) scale(1)');
    });

    it('zooms with the wheel and tells that the user moved', async () => {
      const onUserMove = vi.fn();
      renderFlow({ onUserMove });
      const event = new WheelEvent('wheel', {
        deltaY: -200,
        clientX: 0,
        clientY: 0,
        bubbles: true,
        cancelable: true,
      });
      act(() => {
        screen.getByTestId('flow').dispatchEvent(event);
      });
      expect(event.defaultPrevented).toBe(true);
      expect(layerTransform()).toBe('translate(0px, 0px) scale(1)');
      await frame();
      // One notch is one step of the ladder: 100 % to 125 %.
      expect(layerTransform()).toBe('translate(0px, 0px) scale(1.25)');
      expect(onUserMove).toHaveBeenCalled();
    });

    it('adds up small wheel moves into one step, around the pointer', async () => {
      const onUserMove = vi.fn();
      renderFlow({ onUserMove });
      const send = async (deltaY: number, time: number) => {
        const event = new WheelEvent('wheel', {
          deltaY,
          clientX: 100,
          clientY: 50,
          bubbles: true,
          cancelable: true,
        });
        Object.defineProperty(event, 'timeStamp', { value: time });
        act(() => {
          screen.getByTestId('flow').dispatchEvent(event);
        });
        await frame();
      };
      // Four moves of a quarter notch: no step before the sum is one notch.
      await send(-25, 1000);
      await send(-25, 1010);
      await send(-25, 1020);
      expect(layerTransform()).toBe('translate(0px, 0px) scale(1)');
      expect(onUserMove).not.toHaveBeenCalled();
      await send(-25, 1030);
      expect(layerTransform()).toBe('translate(-25px, -12.5px) scale(1.25)');
      // A fast burst of two notches gives one step for each notch.
      await send(-100, 1040);
      await send(-100, 1050);
      expect(layerTransform()).toMatch(/scale\(1\.5\)|scale\(2\)/);
      // The other way: one step down.
      await send(100, 2000);
      expect(layerTransform()).toMatch(/scale\((1\.25|1\.5)\)/);
    });

    it('pans with the wheel in pan mode, and zooms with Ctrl', async () => {
      const onUserMove = vi.fn();
      renderFlow({ onUserMove, wheel: 'pan' });
      const send = async (init: WheelEventInit) => {
        const event = new WheelEvent('wheel', {
          bubbles: true,
          cancelable: true,
          ...init,
        });
        act(() => {
          screen.getByTestId('flow').dispatchEvent(event);
        });
        await frame();
        return event;
      };
      const down = await send({ deltaY: 120, deltaX: 30 });
      expect(down.defaultPrevented).toBe(true);
      expect(layerTransform()).toBe('translate(-30px, -120px) scale(1)');
      expect(onUserMove).toHaveBeenCalled();

      // A pinch is 5 times finer than a wheel: 20 px make one step.
      await send({ deltaY: -20, ctrlKey: true });
      expect(layerTransform()).toMatch(/scale\(1\.25\)/);
    });

    // Wheel events come faster than frames on some devices: the view changes
    // once for the events of a frame, and reads the place of the canvas once.
    it('changes the view once for the wheel events of one frame', async () => {
      renderFlow({ wheel: 'pan' });
      const writes: string[] = [];
      const layer = screen.getByTestId('flow-layer');
      const observer = new MutationObserver((records) => {
        records.forEach(() => writes.push(layer.style.transform));
      });
      observer.observe(layer, { attributes: true, attributeFilter: ['style'] });
      const place = vi.spyOn(
        screen.getByTestId('flow'),
        'getBoundingClientRect'
      );
      for (const deltaY of [10, 10, 10]) {
        act(() => {
          screen.getByTestId('flow').dispatchEvent(
            new WheelEvent('wheel', {
              deltaY,
              bubbles: true,
              cancelable: true,
            })
          );
        });
      }
      expect(layerTransform()).toBe('translate(0px, 0px) scale(1)');
      await frame();
      observer.takeRecords().forEach(() => writes.push(layer.style.transform));
      observer.disconnect();
      expect(writes).toEqual(['translate(0px, -30px) scale(1)']);
      expect(place).not.toHaveBeenCalled();
    });

    it('reads the place of the canvas once for a burst of zoom steps', async () => {
      renderFlow();
      const place = vi.spyOn(
        screen.getByTestId('flow'),
        'getBoundingClientRect'
      );
      const send = async (time: number) => {
        const event = new WheelEvent('wheel', {
          deltaY: -100,
          bubbles: true,
          cancelable: true,
        });
        Object.defineProperty(event, 'timeStamp', { value: time });
        act(() => {
          screen.getByTestId('flow').dispatchEvent(event);
        });
        await frame();
      };
      await send(1000);
      await send(1050);
      await send(1100);
      expect(place).toHaveBeenCalledTimes(1);
      await send(2000);
      expect(place).toHaveBeenCalledTimes(2);
    });

    it('zooms with the controls', () => {
      renderFlow();
      fireEvent.click(screen.getByLabelText('Zoom in'));
      expect(layerTransform()).toMatch(/scale\(1\.25\)/);
      fireEvent.click(screen.getByLabelText('Zoom out'));
      fireEvent.click(screen.getByLabelText('Zoom out'));
      expect(layerTransform()).toMatch(/scale\(0\.75\)/);
    });
  });

  describe('fit', () => {
    const sizes = { width: 1000, height: 400 };
    let saved: [PropertyDescriptor | undefined, PropertyDescriptor | undefined];

    beforeEach(() => {
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

    it('fits the nodes once, when the canvas has a size', () => {
      renderFlow({ fitOptions: { padding: 0.1, maxZoom: 2 } });
      // The nodes span 540 x 64: 1000 / (540 * 1.2) = 1.54 fits, and the
      // largest step that fits is 150 %.
      const match =
        /translate\(([\d.]+)px, ([\d.]+)px\) scale\(([\d.]+)\)/.exec(
          layerTransform()
        );
      expect(match).not.toBeNull();
      expect(Number(match![3])).toBe(1.5);
    });

    it('keeps a readable zoom for a tall content and shows its top', () => {
      const tall = nodes.map((node) =>
        node.id === 'b' ? { ...node, position: { x: 300, y: 3000 } } : node
      );
      renderFlow({
        nodes: tall,
        fitOptions: { padding: 0.1, maxZoom: 2, readableZoom: 0.5 },
      });
      // The whole fit would be 0.1: the width fit is 1, and the top shows.
      expect(layerTransform()).toMatch(
        /^translate\(([\d.]+)px, 24px\) scale\(1\)$/
      );
    });

    // The wheel of a pan canvas scrolls like an area: the content stays.
    it('keeps the content in reach when the wheel pans far', async () => {
      renderFlow({ wheel: 'pan', fitOptions: { padding: 0.1, maxZoom: 2 } });
      for (let i = 0; i < 6; i += 1) {
        act(() => {
          screen.getByTestId('flow').dispatchEvent(
            new WheelEvent('wheel', {
              deltaY: 3000,
              bubbles: true,
              cancelable: true,
            })
          );
        });
      }
      await frame();
      const match =
        /translate\((-?[\d.]+)px, (-?[\d.]+)px\) scale\(([\d.]+)\)/.exec(
          layerTransform()
        )!;
      const y = Number(match[2]);
      const zoom = Number(match[3]);
      // The nodes span 64 px: they stay at least partly on screen.
      expect(y + 64 * zoom).toBeGreaterThan(0);
    });

    it('fits again with the button', () => {
      renderFlow({ fitOptions: { padding: 0.1, maxZoom: 2 } });
      fireEvent.click(screen.getByLabelText('Zoom in'));
      const zoomed = layerTransform();
      fireEvent.click(screen.getByLabelText('Fit view'));
      expect(layerTransform()).not.toBe(zoomed);
    });
  });

  describe('drag of nodes', () => {
    it('moves a node by the distance of the pointer, over the zoom', () => {
      const onNodesMove = vi.fn();
      const onNodeClick = vi.fn();
      renderFlow({ onNodesMove, onNodeClick });
      fireEvent.click(screen.getByLabelText('Zoom in'));

      pointer(nodeBox('a'), 'pointerdown', 50, 50);
      pointer(window, 'pointermove', 170, 110);
      pointer(window, 'pointerup', 170, 110);
      // 125 % zoom: 120 px on screen are 96 in the flow, 60 are 48.
      expect(onNodesMove).toHaveBeenLastCalledWith({ a: { x: 96, y: 48 } });

      fireEvent.click(nodeBox('a'));
      expect(onNodeClick).not.toHaveBeenCalled();
    });

    it('does not move a node when dragging is off', () => {
      const onNodesMove = vi.fn();
      renderFlow({ onNodesMove, nodesDraggable: false });
      pointer(nodeBox('a'), 'pointerdown', 50, 50);
      pointer(window, 'pointermove', 170, 110);
      pointer(window, 'pointerup', 170, 110);
      expect(onNodesMove).not.toHaveBeenCalled();
      expect(layerTransform()).toBe('translate(0px, 0px) scale(1)');
    });

    it('does not start a drag from a nodrag part', () => {
      const onNodesMove = vi.fn();
      renderFlow({ onNodesMove });
      const handle = handleOf('a', 'source');
      handle.dataset.connectable = 'false';
      pointer(handle, 'pointerdown', 0, 0);
      pointer(window, 'pointermove', 100, 100);
      pointer(window, 'pointerup', 100, 100);
      expect(onNodesMove).not.toHaveBeenCalled();
    });
  });

  describe('links between handles', () => {
    it('connects a source handle to a target handle that is near the drop', () => {
      const onConnect = vi.fn();
      const onConnectEnd = vi.fn();
      renderFlow({ onConnect, onConnectEnd });

      pointer(handleOf('a', 'source'), 'pointerdown', 0, 0);
      pointer(window, 'pointermove', 150, 5);
      expect(screen.getByTestId('flow-connection')).toBeInTheDocument();
      // The target handle of B is at (300, 0): 12 px away is near.
      pointer(window, 'pointerup', 288, 0);

      expect(onConnect).toHaveBeenCalledWith({
        source: 'a',
        sourceHandle: 'source',
        target: 'b',
        targetHandle: 'target',
      });
      expect(onConnectEnd).toHaveBeenCalledWith({
        isValid: true,
        fromNode: { id: 'a' },
        fromHandle: { type: 'source', id: 'source' },
        toNode: { id: 'b' },
      });
      expect(screen.queryByTestId('flow-connection')).toBeNull();
    });

    it('connects from a target handle to a source handle as well', () => {
      const onConnect = vi.fn();
      renderFlow({ onConnect });
      // The source handle of A is at (0, 0).
      pointer(handleOf('b', 'target'), 'pointerdown', 300, 0);
      pointer(window, 'pointermove', 100, 0);
      pointer(window, 'pointerup', 5, 0);
      expect(onConnect).toHaveBeenCalledWith({
        source: 'a',
        sourceHandle: 'source',
        target: 'b',
        targetHandle: 'target',
      });
    });

    it('reports a drop on empty space, and makes no link', () => {
      const onConnect = vi.fn();
      const onConnectEnd = vi.fn();
      renderFlow({ onConnect, onConnectEnd });
      pointer(handleOf('a', 'source'), 'pointerdown', 0, 0);
      pointer(window, 'pointermove', 150, 200);
      pointer(window, 'pointerup', 150, 200);
      expect(onConnect).not.toHaveBeenCalled();
      expect(onConnectEnd).toHaveBeenCalledWith({
        isValid: null,
        fromNode: { id: 'a' },
        fromHandle: { type: 'source', id: 'source' },
        toNode: null,
      });
    });

    it('refuses a link that the caller says is not valid', () => {
      const onConnect = vi.fn();
      const onConnectEnd = vi.fn();
      const isValidConnection = vi.fn(() => false);
      renderFlow({ onConnect, onConnectEnd, isValidConnection });
      pointer(handleOf('a', 'source'), 'pointerdown', 0, 0);
      pointer(window, 'pointermove', 250, 0);
      pointer(window, 'pointerup', 300, 0);
      expect(isValidConnection).toHaveBeenCalledWith(
        expect.objectContaining({ source: 'a', target: 'b' })
      );
      expect(onConnect).not.toHaveBeenCalled();
      expect(onConnectEnd).toHaveBeenCalledWith(
        expect.objectContaining({ isValid: false, toNode: { id: 'b' } })
      );
    });

    it('makes no link and reports no drop for a cancelled drag', () => {
      const onConnect = vi.fn();
      const onConnectEnd = vi.fn();
      renderFlow({ onConnect, onConnectEnd });
      pointer(handleOf('a', 'source'), 'pointerdown', 0, 0);
      pointer(window, 'pointermove', 150, 5);
      // The target handle of B is near: a drop here would link.
      pointer(window, 'pointercancel', 288, 0);
      expect(onConnect).not.toHaveBeenCalled();
      expect(onConnectEnd).not.toHaveBeenCalled();
      expect(screen.queryByTestId('flow-connection')).toBeNull();
    });

    it('treats a click on a handle as nothing', () => {
      const onConnectEnd = vi.fn();
      renderFlow({ onConnectEnd });
      pointer(handleOf('a', 'source'), 'pointerdown', 0, 0);
      pointer(window, 'pointerup', 0, 0);
      expect(onConnectEnd).not.toHaveBeenCalled();
    });

    it('draws no link when connecting is off', () => {
      const onConnectEnd = vi.fn();
      renderFlow({ onConnectEnd, nodesConnectable: false });
      expect(handleOf('a', 'source').dataset.connectable).toBe('false');
      pointer(handleOf('a', 'source'), 'pointerdown', 0, 0);
      pointer(window, 'pointermove', 250, 0);
      pointer(window, 'pointerup', 300, 0);
      expect(onConnectEnd).not.toHaveBeenCalled();
    });
  });

  it('shows the dots, the zoom controls and the overview', () => {
    renderFlow();
    expect(screen.getByTestId('flow-background')).toBeInTheDocument();
    expect(screen.getByLabelText('Fit view')).toBeInTheDocument();
    expect(screen.queryByTestId('flow-minimap')).toBeNull();
  });

  describe('context menus', () => {
    function contextMenu(target: Element, x = 0, y = 0): MouseEvent {
      const event = new MouseEvent('contextmenu', {
        bubbles: true,
        cancelable: true,
        clientX: x,
        clientY: y,
      });
      act(() => {
        target.dispatchEvent(event);
      });
      return event;
    }

    function touch(
      target: Element,
      type: 'pointerdown' | 'pointermove' | 'pointerup',
      x: number,
      y: number
    ) {
      const event = new MouseEvent(type, {
        bubbles: true,
        cancelable: true,
        clientX: x,
        clientY: y,
        button: 0,
      });
      Object.defineProperty(event, 'pointerType', { value: 'touch' });
      act(() => {
        target.dispatchEvent(event);
      });
    }

    function menuHandlers() {
      return {
        onNodeContextMenu: vi.fn(),
        onEdgeContextMenu: vi.fn(),
        onPaneContextMenu: vi.fn(),
      };
    }

    // parity:graph-dblclick-subrun parity:run-graph-dblclick-subrun
    // The node menu carries 'Open sub-run'; this is the event that opens it.
    it('opens the menu of a node, an edge or the pane on right-click', () => {
      const handlers = menuHandlers();
      renderFlow(handlers);
      const onNode = contextMenu(nodeBox('a'), 30, 40);
      expect(handlers.onNodeContextMenu).toHaveBeenCalledWith('a', {
        clientX: 30,
        clientY: 40,
      });
      expect(onNode.defaultPrevented).toBe(true);
      contextMenu(document.querySelector('[data-flow-edge="a-b"]')!, 5, 6);
      expect(handlers.onEdgeContextMenu).toHaveBeenCalledWith('a-b', {
        clientX: 5,
        clientY: 6,
      });
      contextMenu(screen.getByTestId('flow'), 7, 8);
      expect(handlers.onPaneContextMenu).toHaveBeenCalledWith({
        clientX: 7,
        clientY: 8,
      });
      // Each event opens one menu only.
      expect(handlers.onNodeContextMenu).toHaveBeenCalledTimes(1);
      expect(handlers.onEdgeContextMenu).toHaveBeenCalledTimes(1);
      expect(handlers.onPaneContextMenu).toHaveBeenCalledTimes(1);
    });

    it('opens the node menu for a native event from inside the node', () => {
      const handlers = menuHandlers();
      renderFlow(handlers);
      // A button of a node sends a bubbling contextmenu event.
      const inner = nodeBox('b').firstElementChild!;
      contextMenu(inner);
      expect(handlers.onNodeContextMenu).toHaveBeenCalledWith('b', {
        clientX: 0,
        clientY: 0,
      });
      expect(handlers.onPaneContextMenu).not.toHaveBeenCalled();
    });

    it('keeps the browser menu without a handler and on the controls', () => {
      renderFlow();
      expect(contextMenu(nodeBox('a'), 1, 1).defaultPrevented).toBe(false);
      const handlers = menuHandlers();
      renderFlow(handlers);
      const fit = screen.getAllByLabelText('Fit view')[1]!;
      expect(contextMenu(fit, 1, 1).defaultPrevented).toBe(false);
      expect(handlers.onPaneContextMenu).not.toHaveBeenCalled();
    });

    describe('touch long-press', () => {
      beforeEach(() => {
        vi.useFakeTimers();
      });
      afterEach(() => {
        vi.useRealTimers();
      });

      it('opens the node menu after 500 ms and is not a click', () => {
        const handlers = menuHandlers();
        const onNodeClick = vi.fn();
        renderFlow({ ...handlers, onNodeClick });
        touch(nodeBox('a'), 'pointerdown', 20, 20);
        act(() => {
          vi.advanceTimersByTime(499);
        });
        expect(handlers.onNodeContextMenu).not.toHaveBeenCalled();
        act(() => {
          vi.advanceTimersByTime(1);
        });
        expect(handlers.onNodeContextMenu).toHaveBeenCalledWith('a', {
          clientX: 20,
          clientY: 20,
        });
        touch(nodeBox('a'), 'pointerup', 20, 20);
        fireEvent.click(nodeBox('a'));
        expect(onNodeClick).not.toHaveBeenCalled();
      });

      it('opens the pane and edge menus the same way', () => {
        const handlers = menuHandlers();
        renderFlow(handlers);
        touch(screen.getByTestId('flow'), 'pointerdown', 50, 60);
        act(() => {
          vi.advanceTimersByTime(500);
        });
        expect(handlers.onPaneContextMenu).toHaveBeenCalledWith({
          clientX: 50,
          clientY: 60,
        });
        touch(screen.getByTestId('flow'), 'pointerup', 50, 60);
        touch(
          document.querySelector('[data-flow-edge="a-b"]')!,
          'pointerdown',
          5,
          5
        );
        act(() => {
          vi.advanceTimersByTime(500);
        });
        expect(handlers.onEdgeContextMenu).toHaveBeenCalledWith('a-b', {
          clientX: 5,
          clientY: 5,
        });
      });

      it('does not open after a move or an early release', () => {
        const handlers = menuHandlers();
        renderFlow(handlers);
        touch(nodeBox('a'), 'pointerdown', 20, 20);
        touch(nodeBox('a'), 'pointermove', 40, 20);
        act(() => {
          vi.advanceTimersByTime(600);
        });
        touch(nodeBox('a'), 'pointerup', 40, 20);
        touch(nodeBox('b'), 'pointerdown', 20, 20);
        act(() => {
          vi.advanceTimersByTime(200);
        });
        touch(nodeBox('b'), 'pointerup', 20, 20);
        act(() => {
          vi.advanceTimersByTime(600);
        });
        expect(handlers.onNodeContextMenu).not.toHaveBeenCalled();
      });
    });
  });
});
