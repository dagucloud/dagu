// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { components } from '../../../../../api/v1/schema';
import { getHandlers, WorkflowPanel } from '../WorkflowPanel';

vi.mock('../AgentSpecOverview', () => ({
  AgentSpecOverview: () => <div data-testid="agent-overview" />,
}));
vi.mock('../DAGAttributes', () => ({
  default: () => <div data-testid="attributes" />,
}));
// parity:workflow-step-table - the table comes from its own module, not from
// the dag-details barrel, which reaches Mermaid.
vi.mock('../../dag-details/DAGStepTable', () => ({
  default: ({ steps }: { steps: { name: string }[] }) => (
    <ul>
      {steps.map((step) => (
        <li key={step.name}>{step.name}</li>
      ))}
    </ul>
  ),
}));

type DAG = components['schemas']['DAGDetails'];

describe('getHandlers', () => {
  it('lists the handlers in a fixed order and skips the missing ones', () => {
    const dag = {
      handlerOn: {
        exit: { name: 'cleanup' },
        success: { name: 'done' },
      },
    } as DAG;
    expect(getHandlers(dag).map((step) => step.name)).toEqual([
      'done',
      'cleanup',
    ]);
    expect(getHandlers(undefined)).toEqual([]);
  });
});

describe('WorkflowPanel', () => {
  it('shows the description, the attributes and the handlers', () => {
    render(
      <WorkflowPanel
        dag={
          {
            name: 'etl',
            description: 'Loads nightly sales.',
            steps: [{ name: 'extract' }, { name: 'load' }],
            handlerOn: { failure: { name: 'notify' } },
          } as DAG
        }
      />
    );

    expect(screen.getByText('Loads nightly sales.')).toBeInTheDocument();
    expect(screen.getByTestId('attributes')).toBeInTheDocument();
    expect(
      within(screen.getByRole('region', { name: 'Handlers' })).getByText(
        'notify'
      )
    ).toBeInTheDocument();
  });

  // The steps are on the canvas: the Overview has no table of them.
  it('has no table of steps', () => {
    render(
      <WorkflowPanel
        dag={
          { name: 'etl', steps: [{ name: 'extract' }, { name: 'load' }] } as DAG
        }
      />
    );

    expect(screen.queryByRole('region', { name: 'Steps' })).toBeNull();
    expect(screen.queryByText('extract')).toBeNull();
    expect(screen.queryByTestId('ve-workflow-steps')).toBeNull();
  });

  it('leaves out what the workflow does not have', () => {
    render(<WorkflowPanel dag={{ name: 'empty' } as DAG} />);

    expect(screen.getByTestId('attributes')).toBeInTheDocument();
    expect(screen.queryByRole('region')).not.toBeInTheDocument();
  });

  // The steps of an agent DAG are the actions that the agent can choose, with
  // its tasks: the overview of the agent shows them.
  it('shows the tasks and actions of an agent DAG', () => {
    render(
      <WorkflowPanel
        dag={
          {
            name: 'ship',
            type: 'agent',
            steps: [{ name: 'plan' }],
            tasks: [{ name: 'planned', description: 'Plan exists.' }],
          } as DAG
        }
      />
    );

    expect(screen.getByTestId('agent-overview')).toBeInTheDocument();
    expect(screen.getByTestId('attributes')).toBeInTheDocument();
  });

  // jsdom has no container queries: the test pins the contract of the
  // class, which stacks the rows in a panel narrower than 30rem.
  it('stacks the handler table in a narrow panel and never clips it', () => {
    render(
      <WorkflowPanel
        dag={
          {
            name: 'etl',
            handlerOn: { failure: { name: 'notify' } },
          } as DAG
        }
      />
    );

    const box = screen.getByTestId('ve-workflow-handlers');
    expect(box).toHaveClass('@container', 'min-w-0');
    expect(box).not.toHaveClass('overflow-hidden');
    expect(box.className).toContain('@max-[30rem]:[&_table]:min-w-0');
    expect(box.className).toContain('@max-[30rem]:[&_thead]:hidden');
  });
});
