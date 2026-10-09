// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { components, NodeStatus, NodeStatusLabel } from '@/api/v1/schema';
import { toMermaidNodeId } from '@/lib/utils';
import Graph from '../Graph';

const mermaidRenderMock = vi.hoisted(() => vi.fn());
const downloadBlobMock = vi.hoisted(() => vi.fn());

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: mermaidRenderMock,
  },
}));

vi.mock('@/lib/download', () => ({
  downloadBlob: downloadBlobMock,
}));

vi.mock('@/contexts/UserPreference', () => ({
  useUserPreferences: () => ({ preferences: { theme: 'light' } }),
}));

beforeEach(() => {
  mermaidRenderMock.mockReset();
  downloadBlobMock.mockReset();
});

function node(
  name: string,
  status: NodeStatus,
  depends?: string[]
): components['schemas']['Node'] {
  return {
    step: { name, depends },
    stdout: '',
    stderr: '',
    startedAt: '',
    finishedAt: '',
    status,
    statusLabel: NodeStatusLabel.not_started,
    retryCount: 0,
    doneCount: 0,
  };
}

describe('Graph', () => {
  it('uses the darker success color visible in the execution graph', async () => {
    mermaidRenderMock.mockResolvedValueOnce({
      svg: '<svg></svg>',
      bindFunctions: vi.fn(),
    });

    render(
      <Graph
        type="status"
        steps={[
          node('prepare', NodeStatus.Success),
          node('load', NodeStatus.Success, ['prepare']),
        ]}
      />
    );

    await waitFor(() => {
      expect(mermaidRenderMock).toHaveBeenCalled();
    });

    const firstCall = mermaidRenderMock.mock.calls[0];
    if (!firstCall) {
      throw new Error('Expected mermaid.render to be called');
    }
    const definition = firstCall[1] as string;
    expect(definition).toContain(
      'classDef done color:#14161b,fill:#fbfaf6,stroke:#22c55e'
    );
    expect(definition).toContain(
      'linkStyle 0 stroke:#3fa76b,stroke-width:1.8px'
    );
    expect(definition).not.toContain('#1e8e3e');
    expect(definition).not.toContain('#7da87d');
  });

  it('uses the darker running color in the Mermaid graph definition', async () => {
    mermaidRenderMock.mockResolvedValueOnce({
      svg: '<svg></svg>',
      bindFunctions: vi.fn(),
    });

    render(
      <Graph
        type="status"
        steps={[node('load', NodeStatus.Running)]}
      />
    );

    await waitFor(() => {
      expect(mermaidRenderMock).toHaveBeenCalled();
    });

    const firstCall = mermaidRenderMock.mock.calls[0];
    if (!firstCall) {
      throw new Error('Expected mermaid.render to be called');
    }
    const definition = firstCall[1] as string;
    expect(definition).toContain(
      'classDef running color:#14161b,fill:#fbfaf6,stroke:#7c6ef4'
    );
    expect(definition).not.toContain('#81c784');
  });

  it('forces darker status strokes onto the rendered Mermaid SVG', async () => {
    mermaidRenderMock.mockResolvedValueOnce({
      svg: `
        <svg>
          <g class="node done"><rect data-testid="done-node"></rect></g>
          <g class="node running"><rect data-testid="running-node"></rect></g>
        </svg>
      `,
      bindFunctions: vi.fn(),
    });

    render(
      <Graph
        type="status"
        steps={[
          node('prepare', NodeStatus.Success),
          node('load', NodeStatus.Running),
        ]}
      />
    );

    const doneNode = await screen.findByTestId('done-node');
    const runningNode = await screen.findByTestId('running-node');

    expect(doneNode).toHaveAttribute('stroke', '#22c55e');
    expect(doneNode).toHaveAttribute('stroke-width', '2.5px');
    expect(runningNode).toHaveAttribute('stroke', '#7c6ef4');
    expect(runningNode).toHaveAttribute('stroke-width', '2.5px');
  });

  it('labels a step with its name when it also has an id', async () => {
    mermaidRenderMock.mockResolvedValueOnce({
      svg: '<svg></svg>',
      bindFunctions: vi.fn(),
    });

    render(
      <Graph
        type="config"
        steps={[
          { name: 'Get Date', id: 'date' },
          { name: 'Use Date', depends: ['Get Date'] },
        ]}
      />
    );

    await waitFor(() => {
      expect(mermaidRenderMock).toHaveBeenCalled();
    });

    const firstCall = mermaidRenderMock.mock.calls[0];
    if (!firstCall) {
      throw new Error('Expected mermaid.render to be called');
    }
    const definition = firstCall[1] as string;
    expect(definition).toContain('["Get Date"]');
    expect(definition).not.toContain('["date"]');
  });

  it('labels a fallback step with its name when it also has an id', async () => {
    mermaidRenderMock.mockRejectedValueOnce(new TypeError('render exploded'));

    render(<Graph type="config" steps={[{ name: 'Get Date', id: 'date' }]} />);

    const fallback = await screen.findByTestId('graph-fallback');
    expect(fallback).toHaveTextContent('Get Date');
  });

  it('renders an interactive fallback when Mermaid rendering fails', async () => {
    mermaidRenderMock.mockRejectedValueOnce(new TypeError('render exploded'));
    const onClickNode = vi.fn();

    render(
      <Graph
        type="status"
        steps={[
          node('extract (source)', NodeStatus.Success),
          node('load:warehouse', NodeStatus.NotStarted, ['extract (source)']),
        ]}
        onClickNode={onClickNode}
        selectOnClick
      />
    );

    const fallback = await screen.findByTestId('graph-fallback');
    expect(fallback).toHaveTextContent('extract (source)');
    expect(fallback).toHaveTextContent('load:warehouse');
    expect(
      screen.queryByText(/Error rendering diagram/i)
    ).not.toBeInTheDocument();

    fireEvent.click(
      screen.getByRole('button', { name: /inspect extract \(source\)/i })
    );

    await waitFor(() => {
      expect(onClickNode).toHaveBeenCalledWith(
        toMermaidNodeId('extract (source)')
      );
    });
  });

  it('exports the rendered graph as a self-contained SVG', async () => {
    mermaidRenderMock.mockResolvedValueOnce({
      svg: '<svg viewBox="0 0 200 100"><g class="node done"><rect></rect><foreignObject x="10" y="6" width="60" height="20"><div xmlns="http://www.w3.org/1999/xhtml">prepare</div></foreignObject></g></svg>',
      bindFunctions: vi.fn(),
    });

    render(
      <Graph
        type="status"
        steps={[node('prepare', NodeStatus.Success)]}
        name="mydag"
      />
    );

    await waitFor(() => {
      expect(mermaidRenderMock).toHaveBeenCalled();
    });

    fireEvent.click(screen.getByRole('button', { name: 'Export as SVG' }));

    expect(downloadBlobMock).toHaveBeenCalledTimes(1);
    const call = downloadBlobMock.mock.calls[0];
    if (!call) {
      throw new Error('Expected downloadBlob to be called');
    }
    const [blob, filename] = call as [Blob, string];
    expect(filename).toBe('mydag-graph.svg');
    expect(blob.type).toBe('image/svg+xml');
    const markup = await blob.text();
    expect(markup).toContain('width="200"');
    expect(markup).toContain('height="100"');
    expect(markup).toContain('<rect');
    // The on-screen zoom size is not part of the exported image.
    const exported = new DOMParser().parseFromString(
      markup,
      'image/svg+xml'
    ).documentElement;
    expect(exported.getAttribute('style') ?? '').not.toMatch(
      /(^|;)\s*(width|height):/
    );
    // HTML labels become native text so PNG rasterization is not tainted
    // and non-browser SVG tools render the labels.
    expect(markup).toContain('>prepare</text>');
    expect(markup).not.toContain('foreignObject');
    // Centered inside the source foreignObject rectangle (x + w/2, y + h/2).
    expect(markup).toContain('x="40"');
    expect(markup).toContain('y="16"');
    // Inline styles beat mermaid's shape-oriented stylesheet inside the SVG.
    expect(markup).toContain('stroke: none');

    // PNG export needs canvas rasterization, unavailable in jsdom; the
    // control itself must still be present.
    expect(
      screen.getByRole('button', { name: 'Export as PNG' })
    ).toBeInTheDocument();
  });

  it('keeps graph controls constrained above the graph on narrow screens', async () => {
    mermaidRenderMock.mockResolvedValueOnce({
      svg: '<svg></svg>',
      bindFunctions: vi.fn(),
    });

    const { container } = render(
      <Graph
        type="status"
        steps={[node('load', NodeStatus.Running)]}
        flowchart="LR"
        onChangeFlowchart={vi.fn()}
      />
    );

    const controls = screen.getByRole('group', { name: 'Graph controls' });
    expect(controls).toHaveClass('min-w-max');

    const controlsViewport = controls.parentElement;
    expect(controlsViewport).toHaveClass('inset-x-2');
    expect(controlsViewport).toHaveClass('max-w-[calc(100%-1rem)]');
    expect(controlsViewport).toHaveClass('overflow-x-auto');

    expect(
      screen.getByRole('button', { name: 'Horizontal layout' })
    ).toHaveClass('w-9');
    expect(screen.getByRole('button', { name: 'Expand graph' })).toHaveClass(
      'w-9'
    );

    expect(container.querySelector('.custom-scrollbar')).toHaveClass('pt-14');
  });
  it('draws inferred dependencies as dashed arrows after explicit ones', async () => {
    mermaidRenderMock.mockResolvedValueOnce({
      svg: '<svg></svg>',
      bindFunctions: vi.fn(),
    });

    const consumer = node('deploy', NodeStatus.Success, ['prepare']);
    consumer.step.inferredDepends = ['build'];

    render(
      <Graph
        type="status"
        steps={[
          node('prepare', NodeStatus.Success),
          node('build', NodeStatus.Success),
          consumer,
        ]}
      />
    );

    await waitFor(() => {
      expect(mermaidRenderMock).toHaveBeenCalled();
    });

    const firstCall = mermaidRenderMock.mock.calls[0];
    if (!firstCall) {
      throw new Error('Expected mermaid.render to be called');
    }
    const definition = firstCall[1] as string;
    expect(definition).toContain(
      `${toMermaidNodeId('prepare')} --> ${toMermaidNodeId('deploy')};`
    );
    expect(definition).toContain(
      `${toMermaidNodeId('build')} -.-> ${toMermaidNodeId('deploy')};`
    );
    expect(definition).toContain(
      'linkStyle 0 stroke:#3fa76b,stroke-width:1.8px'
    );
    expect(definition).toContain(
      'linkStyle 1 stroke:#3fa76b,stroke-width:1.8px,stroke-dasharray:6 3'
    );
  });
});

describe('Graph zoom', () => {
  const LARGE_SVG = '<svg viewBox="0 0 4000 1000"></svg>';
  const SMALL_SVG = '<svg viewBox="0 0 200 100"></svg>';

  // jsdom has no layout engine, so every element reports an 800x400 box
  // with no inset around the viewport.
  beforeEach(() => {
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(400);
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(800);
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(400);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  function svgWidth(container: HTMLElement): string {
    return (
      container.querySelector<SVGSVGElement>('.mermaid svg')?.style.width ?? ''
    );
  }

  function boxHeight(container: HTMLElement): string {
    return (
      viewportOf(container).parentElement?.parentElement?.style.height ?? ''
    );
  }

  function viewportOf(container: HTMLElement): HTMLElement {
    const viewport = container.querySelector('.mermaid')?.parentElement;
    if (!viewport) {
      throw new Error('Expected the graph viewport');
    }
    return viewport;
  }

  async function renderGraph(
    svg: string,
    props: Partial<React.ComponentProps<typeof Graph>> = {}
  ) {
    mermaidRenderMock.mockResolvedValue({ svg, bindFunctions: vi.fn() });
    const steps = [node('prepare', NodeStatus.Running)];
    const view = render(<Graph type="status" steps={steps} {...props} />);
    await waitFor(() => expect(svgWidth(view.container)).not.toBe(''));
    return view;
  }

  // The 736x336 area inside the padding would need 18% to show 4000x1000;
  // the first view stops at the readable minimum instead.
  it('opens a large graph at a readable zoom and fits it on request', async () => {
    const { container } = await renderGraph(LARGE_SVG);

    expect(svgWidth(container)).toBe('2400px');
    expect(
      screen.getByRole('button', { name: 'Reset zoom' })
    ).toHaveTextContent('60%');

    fireEvent.click(screen.getByRole('button', { name: 'Fit to screen' }));
    expect(svgWidth(container)).toBe('736px');
  });

  it('never enlarges a small graph to fit', async () => {
    const { container } = await renderGraph(SMALL_SVG);

    expect(svgWidth(container)).toBe('200px');
  });

  it('keeps the zoom when only step statuses change', async () => {
    const { container, rerender } = await renderGraph(LARGE_SVG);
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(svgWidth(container)).toBe('2880px');

    mermaidRenderMock.mockResolvedValue({
      svg: '<svg data-render="update" viewBox="0 0 4000 1000"></svg>',
      bindFunctions: vi.fn(),
    });
    rerender(
      <Graph type="status" steps={[node('prepare', NodeStatus.Success)]} />
    );

    await waitFor(() =>
      expect(
        container.querySelector('svg[data-render="update"]')
      ).not.toBeNull()
    );
    expect(svgWidth(container)).toBe('2880px');
  });

  it('fits again when the layout direction changes', async () => {
    const steps = [node('prepare', NodeStatus.Running)];
    const { container, rerender } = await renderGraph(LARGE_SVG, { steps });
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(svgWidth(container)).toBe('2880px');

    rerender(<Graph type="status" steps={steps} flowchart="LR" />);

    await waitFor(() => expect(svgWidth(container)).toBe('2400px'));
  });

  it('zooms with Ctrl+wheel and leaves plain wheel scrolling alone', async () => {
    const { container } = await renderGraph(LARGE_SVG);
    const viewport = viewportOf(container);

    expect(fireEvent.wheel(viewport, { deltaY: 100 })).toBe(true);
    expect(svgWidth(container)).toBe('2400px');

    expect(fireEvent.wheel(viewport, { deltaY: -100, ctrlKey: true })).toBe(
      false
    );
    expect(parseFloat(svgWidth(container))).toBeGreaterThan(2400);
  });

  it('pans on mouse drag without selecting the node under the cursor', async () => {
    const onClickNode = vi.fn();
    const nodeId = toMermaidNodeId('prepare');
    const { container } = await renderGraph(
      `<svg viewBox="0 0 4000 1000"><g class="node" id="flowchart-${nodeId}-0"></g></svg>`,
      { onClickNode, selectOnClick: true }
    );
    const viewport = viewportOf(container);
    const graphNode = container.querySelector('.node');
    if (!graphNode) {
      throw new Error('Expected a rendered node');
    }
    const pointer = { pointerType: 'mouse', pointerId: 1, button: 0 };
    viewport.scrollLeft = 200;

    fireEvent.pointerDown(graphNode, {
      ...pointer,
      buttons: 1,
      clientX: 100,
      clientY: 50,
    });
    fireEvent.pointerMove(graphNode, {
      ...pointer,
      buttons: 1,
      clientX: 160,
      clientY: 50,
    });
    fireEvent.pointerUp(graphNode, { ...pointer, clientX: 160, clientY: 50 });
    fireEvent.click(graphNode);

    expect(viewport.scrollLeft).toBe(140);
    // Node clicks fire after a 250ms double-click window.
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(onClickNode).not.toHaveBeenCalled();

    fireEvent.pointerDown(graphNode, { ...pointer, clientX: 100, clientY: 50 });
    fireEvent.pointerUp(graphNode, { ...pointer, clientX: 100, clientY: 50 });
    fireEvent.click(graphNode);

    await waitFor(() => expect(onClickNode).toHaveBeenCalledWith(nodeId));
  });

  // jsdom's 768px window caps a content-sized box at 60%, or 461px.
  it('sizes the box to the graph up to a share of the window', async () => {
    const small = await renderGraph(SMALL_SVG);
    expect(boxHeight(small.container)).toBe('200px');
    small.unmount();

    const large = await renderGraph(LARGE_SVG);
    expect(boxHeight(large.container)).toBe('461px');
  });

  it('keeps an explicit box height', async () => {
    const { container } = await renderGraph(SMALL_SVG, { height: 300 });

    expect(boxHeight(container)).toBe('300px');
  });

  // The release happened outside the graph, so the graph only sees the
  // mouse coming back with no button held.
  it('does not pan after the button was released outside the graph', async () => {
    const { container } = await renderGraph(LARGE_SVG);
    const viewport = viewportOf(container);
    const pointer = { pointerType: 'mouse', pointerId: 1, button: 0 };
    viewport.scrollLeft = 200;

    fireEvent.pointerDown(viewport, {
      ...pointer,
      buttons: 1,
      clientX: 100,
      clientY: 50,
    });
    fireEvent.pointerMove(viewport, {
      ...pointer,
      buttons: 0,
      clientX: 300,
      clientY: 50,
    });

    expect(viewport.scrollLeft).toBe(200);
  });

  // 40000px wide needs 1.84% to fit the 736px inside the padding.
  it('fits a very long graph below the zoom floor and stays there', async () => {
    const { container } = await renderGraph(
      '<svg viewBox="0 0 40000 1000"></svg>'
    );

    fireEvent.click(screen.getByRole('button', { name: 'Fit to screen' }));
    expect(svgWidth(container)).toBe('736px');

    fireEvent.click(screen.getByRole('button', { name: 'Zoom out' }));
    expect(svgWidth(container)).toBe('736px');

    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(parseFloat(svgWidth(container))).toBeCloseTo(883.2);
  });

  it('fits again when a dependency changes', async () => {
    const steps = [
      node('a', NodeStatus.Running),
      node('b', NodeStatus.Running, ['a']),
      node('c', NodeStatus.Running, ['b']),
    ];
    const { container, rerender } = await renderGraph(LARGE_SVG, { steps });
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(svgWidth(container)).toBe('2880px');

    rerender(
      <Graph
        type="status"
        steps={[steps[0]!, steps[1]!, node('c', NodeStatus.Running, ['a'])]}
      />
    );

    await waitFor(() => expect(svgWidth(container)).toBe('2400px'));
  });
});
