// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { components, NodeStatus, NodeStatusLabel } from '@/api/v1/schema';
import { toMermaidNodeId } from '@/lib/utils';
import Graph from '../Graph';

const mermaidRenderMock = vi.hoisted(() => vi.fn());

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: mermaidRenderMock,
  },
}));

vi.mock('@/contexts/UserPreference', () => ({
  useUserPreferences: () => ({ preferences: { theme: 'light' } }),
}));

beforeEach(() => {
  mermaidRenderMock.mockReset();
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
});
