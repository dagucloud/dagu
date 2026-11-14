// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import Mermaid from '../mermaid';

type RenderResult = {
  svg: string;
  bindFunctions?: (element: Element) => void;
};

type PendingRender = {
  def: string;
  resolve: (value: RenderResult) => void;
  reject: (error: unknown) => void;
};

const mermaidRenderMock = vi.hoisted(() => vi.fn());

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: mermaidRenderMock,
  },
}));

describe('Mermaid', () => {
  let pendingRenders: PendingRender[];

  function pendingRenderAt(index: number): PendingRender {
    const pendingRender = pendingRenders[index];
    if (!pendingRender) {
      throw new Error(`Missing pending render at index ${index}`);
    }
    return pendingRender;
  }

  beforeEach(() => {
    vi.useRealTimers();
    pendingRenders = [];
    mermaidRenderMock.mockReset();
    mermaidRenderMock.mockImplementation(
      (_id: string, def: string) =>
        new Promise<RenderResult>((resolve, reject) => {
          pendingRenders.push({ def, resolve, reject });
        })
    );
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('ignores stale render results after a newer definition renders', async () => {
    const { container, rerender } = render(
      <Mermaid def="graph TD; A-->B;" scale={1} />
    );

    await waitFor(() => expect(pendingRenders).toHaveLength(1));
    rerender(<Mermaid def="graph TD; C-->D;" scale={1} />);
    await waitFor(() => expect(pendingRenders).toHaveLength(2));

    await act(async () => {
      pendingRenderAt(1).resolve({ svg: '<svg data-def="newer"></svg>' });
    });

    await waitFor(() => {
      expect(container.querySelector('svg[data-def="newer"]')).not.toBeNull();
    });

    await act(async () => {
      pendingRenderAt(0).resolve({ svg: '<svg data-def="stale"></svg>' });
    });

    expect(container.querySelector('svg[data-def="newer"]')).not.toBeNull();
    expect(container.querySelector('svg[data-def="stale"]')).toBeNull();
  });

  it('ignores stale render errors after a newer definition renders', async () => {
    const { rerender } = render(
      <Mermaid
        def="graph TD; A-->B;"
        fallback={<div>Fallback graph</div>}
        scale={1}
      />
    );

    await waitFor(() => expect(pendingRenders).toHaveLength(1));
    rerender(
      <Mermaid
        def="graph TD; C-->D;"
        fallback={<div>Fallback graph</div>}
        scale={1}
      />
    );
    await waitFor(() => expect(pendingRenders).toHaveLength(2));

    await act(async () => {
      pendingRenderAt(1).resolve({ svg: '<svg data-def="newer"></svg>' });
    });
    await waitFor(() => {
      expect(screen.queryByText('Fallback graph')).not.toBeInTheDocument();
    });

    await act(async () => {
      pendingRenderAt(0).reject(new Error('stale failure'));
    });

    expect(screen.queryByText('Fallback graph')).not.toBeInTheDocument();
  });

  // Re-rendering removes the old SVG before the new one is ready. Content
  // below the graph must not collapse and jump in the meantime. jsdom has no
  // layout engine, so the rendered height is stubbed.
  it('keeps its height while a new definition renders', async () => {
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(240);
    const { container, rerender } = render(
      <Mermaid def="graph TD; A-->B;" scale={1} />
    );
    await waitFor(() => expect(pendingRenders).toHaveLength(1));
    await act(async () => {
      pendingRenderAt(0).resolve({ svg: '<svg data-def="first"></svg>' });
    });
    const graph = container.querySelector<HTMLElement>('.mermaid');
    const restingMinHeight = graph?.style.minHeight;

    rerender(<Mermaid def="graph TD; C-->D;" scale={1} />);
    await waitFor(() => expect(pendingRenders).toHaveLength(2));
    expect(graph?.style.minHeight).toBe('240px');

    await act(async () => {
      pendingRenderAt(1).resolve({ svg: '<svg data-def="second"></svg>' });
    });
    expect(graph?.style.minHeight).toBe(restingMinHeight);
  });

  it('sizes the drawing from its viewBox at the current scale', async () => {
    const { container, rerender } = render(
      <Mermaid def="graph TD; A-->B;" scale={0.5} />
    );
    await waitFor(() => expect(pendingRenders).toHaveLength(1));
    await act(async () => {
      pendingRenderAt(0).resolve({ svg: '<svg viewBox="0 0 200 100"></svg>' });
    });
    const svg = container.querySelector('svg');
    expect(svg?.style.width).toBe('100px');
    expect(svg?.style.height).toBe('50px');

    rerender(<Mermaid def="graph TD; A-->B;" scale={2} />);
    expect(svg?.style.width).toBe('400px');
    expect(svg?.style.height).toBe('200px');
  });

  it('applies a zoom made while a render is pending', async () => {
    const { container, rerender } = render(
      <Mermaid def="graph TD; A-->B;" scale={1} />
    );
    await waitFor(() => expect(pendingRenders).toHaveLength(1));
    rerender(<Mermaid def="graph TD; A-->B;" scale={2} />);

    await act(async () => {
      pendingRenderAt(0).resolve({ svg: '<svg viewBox="0 0 200 100"></svg>' });
    });

    expect(container.querySelector('svg')?.style.width).toBe('400px');
  });

  it('leaves an SVG without a size unscaled', async () => {
    const { container } = render(
      <Mermaid
        def="graph TD; A-->B;"
        fallback={<div>Fallback graph</div>}
        scale={2}
      />
    );
    await waitFor(() => expect(pendingRenders).toHaveLength(1));
    await act(async () => {
      pendingRenderAt(0).resolve({ svg: '<svg data-def="unsized"></svg>' });
    });

    expect(container.querySelector('svg')?.style.width).toBe('');
    expect(screen.queryByText('Fallback graph')).not.toBeInTheDocument();
  });

  it('reports the diagram size, or null when rendering fails', async () => {
    const onContentSize = vi.fn();
    const { rerender } = render(
      <Mermaid def="graph TD; A-->B;" onContentSize={onContentSize} scale={1} />
    );
    await waitFor(() => expect(pendingRenders).toHaveLength(1));
    await act(async () => {
      pendingRenderAt(0).resolve({ svg: '<svg viewBox="0 0 200 100"></svg>' });
    });
    expect(onContentSize).toHaveBeenLastCalledWith({ width: 200, height: 100 });

    rerender(
      <Mermaid def="graph TD; C-->D;" onContentSize={onContentSize} scale={1} />
    );
    await waitFor(() => expect(pendingRenders).toHaveLength(2));
    await act(async () => {
      pendingRenderAt(1).reject(new Error('broken definition'));
    });
    expect(onContentSize).toHaveBeenLastCalledWith(null);
  });

  it('resolves Mermaid 11.15 prefixed node ids before firing graph callbacks', async () => {
    vi.useFakeTimers();
    const onClick = vi.fn();
    const onDoubleClick = vi.fn();
    const onRightClick = vi.fn();
    const firstNodeId = 'node_66_69_72_73_74';
    const secondNodeId = 'node_73_65_63_6f_6e_64';
    const thirdNodeId = 'node_74_68_69_72_64';

    const { container } = render(
      <Mermaid
        def="graph TD; A-->B;"
        nodeIds={[firstNodeId, secondNodeId, thirdNodeId]}
        onClick={onClick}
        onDoubleClick={onDoubleClick}
        onRightClick={onRightClick}
        scale={1}
      />
    );

    await act(async () => {
      pendingRenderAt(0).resolve({
        svg: `
          <svg>
            <g class="node" id="mermaid-random-flowchart-${firstNodeId}-0"></g>
            <g class="node" id="mermaid-random-flowchart-${secondNodeId}-1"></g>
            <g class="node" id="mermaid-random-flowchart-${thirdNodeId}-2"></g>
          </svg>
        `,
      });
    });

    const nodes = container.querySelectorAll('.node');
    expect(nodes).toHaveLength(3);

    fireEvent.click(nodes.item(0));
    await act(async () => {
      vi.advanceTimersByTime(250);
    });
    fireEvent.dblClick(nodes.item(1));
    fireEvent.contextMenu(nodes.item(2));

    expect(onClick).toHaveBeenCalledWith(firstNodeId);
    expect(onDoubleClick).toHaveBeenCalledWith(secondNodeId);
    expect(onRightClick).toHaveBeenCalledWith(thirdNodeId);
  });

  it('keeps legacy Mermaid node ids working', async () => {
    const onRightClick = vi.fn();
    const nodeId = 'node_6c_65_67_61_63_79';

    const { container } = render(
      <Mermaid
        def="graph TD; A-->B;"
        nodeIds={[nodeId]}
        onRightClick={onRightClick}
        scale={1}
      />
    );

    await act(async () => {
      pendingRenderAt(0).resolve({
        svg: `<svg><g class="node" id="flowchart-${nodeId}-0"></g></svg>`,
      });
    });

    const node = container.querySelector('.node');
    expect(node).not.toBeNull();
    fireEvent.contextMenu(node!);

    expect(onRightClick).toHaveBeenCalledWith(nodeId);
  });

  it('prefers the longest known node id when ids share a prefix', async () => {
    const onRightClick = vi.fn();
    const shortNodeId = 'node_61';
    const longNodeId = 'node_61_62';

    const { container } = render(
      <Mermaid
        def="graph TD; A-->B;"
        nodeIds={[shortNodeId, longNodeId]}
        onRightClick={onRightClick}
        scale={1}
      />
    );

    await act(async () => {
      pendingRenderAt(0).resolve({
        svg: `<svg><g class="node" id="mermaid-random-flowchart-${longNodeId}-0"></g></svg>`,
      });
    });

    const node = container.querySelector('.node');
    expect(node).not.toBeNull();
    fireEvent.contextMenu(node!);

    expect(onRightClick).toHaveBeenCalledWith(longNodeId);
  });

  it('does not match a known node id inside a longer rendered node id', async () => {
    const onRightClick = vi.fn();
    const knownNodeId = 'node_61';

    const { container } = render(
      <Mermaid
        def="graph TD; A-->B;"
        nodeIds={[knownNodeId]}
        onRightClick={onRightClick}
        scale={1}
      />
    );

    await act(async () => {
      pendingRenderAt(0).resolve({
        svg: '<svg><g class="node" id="mermaid-random-flowchart-node_61_62-0"></g></svg>',
      });
    });

    const node = container.querySelector<HTMLElement>('.node');
    expect(node).not.toBeNull();
    expect(node!.style.cursor).toBe('');
    fireEvent.contextMenu(node!);

    expect(onRightClick).not.toHaveBeenCalled();
  });
});
