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
        showIcons={false}
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
      'classDef done color:#0f1129,fill:#ffffff,stroke:#166534'
    );
    expect(definition).toContain(
      'linkStyle 0 stroke:#5f8f64,stroke-width:1.8px'
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
        showIcons={false}
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
      'classDef running color:#0f1129,fill:#ffffff,stroke:#43a047'
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
        showIcons={false}
      />
    );

    const doneNode = await screen.findByTestId('done-node');
    const runningNode = await screen.findByTestId('running-node');

    expect(doneNode).toHaveAttribute('stroke', '#166534');
    expect(doneNode).toHaveAttribute('stroke-width', '2.5px');
    expect(runningNode).toHaveAttribute('stroke', '#43a047');
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
});
