// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  RightPanelProvider,
  useActiveRightPanel,
} from '@/contexts/RightPanelContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { NodeStatus } from '@/api/v1/schema';
import type { ApiDAGDetails, RunOverlay } from '../contracts';
import DetailsCanvas from '../DetailsCanvas';

const dag = {
  name: 'child',
  steps: [
    { name: 'say', commands: [{ command: 'echo', args: ['hi'] }] },
    { name: 'shout', depends: ['say'] },
  ],
} as ApiDAGDetails;

function renderCanvas(
  props: Partial<React.ComponentProps<typeof DetailsCanvas>> = {}
) {
  const onSelectStep = vi.fn();
  render(
    <UserPreferencesProvider>
      <DetailsCanvas dag={dag} onSelectStep={onSelectStep} {...props} />
    </UserPreferencesProvider>
  );
  return { onSelectStep };
}

function ActivePanel() {
  return <output>{useActiveRightPanel() ?? 'none'}</output>;
}

// parity:spec-subdag-readonly-canvas
describe('DetailsCanvas', () => {
  it('draws the steps of the DAG on a read-only canvas', () => {
    renderCanvas();

    expect(screen.getByTestId('ve-canvas-node-say')).toBeInTheDocument();
    expect(screen.getByTestId('ve-canvas-node-shout')).toBeInTheDocument();
    expect(screen.getByText('Read-only')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Add a step' })
    ).not.toBeInTheDocument();
  });

  it('tells the caller which step was picked, by its name', () => {
    const { onSelectStep } = renderCanvas();

    fireEvent.click(screen.getByTestId('ve-canvas-node-shout'));
    expect(onSelectStep).toHaveBeenLastCalledWith('shout');
    fireEvent.click(screen.getByTestId('flow'));
    expect(onSelectStep).toHaveBeenLastCalledWith(null);
  });

  it('puts the controls of the caller in the toolbar', () => {
    renderCanvas({ toolbarEnd: <button type="button">Mine</button> });

    expect(screen.getByTestId('ve-toolbar')).toContainElement(
      screen.getByText('Mine')
    );
  });

  // The controls keep the order of the editor toolbar: Workflow, then trail.
  it('puts the trail after the Workflow button', () => {
    renderCanvas({
      toolbarTrail: <button type="button">Trail</button>,
      workflowPanel: <p>Panel</p>,
    });

    const labels = within(screen.getByTestId('ve-toolbar'))
      .getAllByRole('button')
      .map((button) => button.getAttribute('aria-label') ?? button.textContent);
    const at = (text: string) => labels.findIndex((l) => l?.includes(text));
    expect(at('Workflow')).toBeLessThan(at('Trail'));
  });

  // The start controls, such as the tabs of a file, lead the canvas controls.
  it('puts the start controls before the canvas controls', () => {
    renderCanvas({ toolbarStart: <button type="button">Start</button> });

    const start = screen.getByRole('button', { name: 'Start' });
    expect(
      start.compareDocumentPosition(screen.getByLabelText('Top to bottom')) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('starts top to bottom and switches the direction', () => {
    renderCanvas();

    const tb = screen.getByLabelText('Top to bottom');
    expect(tb).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByLabelText('Left to right'));
    expect(screen.getByLabelText('Left to right')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    expect(tb).toHaveAttribute('aria-pressed', 'false');
  });

  // parity:workflow-panel
  it('opens and closes the workflow panel when it has one', () => {
    renderCanvas({ workflowPanel: <p>About child</p> });

    expect(screen.queryByText('About child')).not.toBeInTheDocument();
    const button = screen.getByRole('button', { name: 'Workflow' });
    fireEvent.click(button);
    expect(screen.getByText('About child')).toBeInTheDocument();
    expect(button).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(button).toHaveAttribute('aria-pressed', 'false');
  });

  // parity:drawer-mode-rules
  it('claims the workflow panel id while the panel is open', () => {
    render(
      <UserPreferencesProvider>
        <RightPanelProvider>
          <DetailsCanvas
            dag={dag}
            onSelectStep={vi.fn()}
            workflowPanel={<p>About child</p>}
          />
          <ActivePanel />
        </RightPanelProvider>
      </UserPreferencesProvider>
    );

    expect(screen.getByRole('status')).toHaveTextContent('none');
    fireEvent.click(screen.getByRole('button', { name: 'Workflow' }));
    expect(screen.getByRole('status')).toHaveTextContent('workflow');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.getByRole('status')).toHaveTextContent('none');
  });

  it('has no workflow button without a panel', () => {
    renderCanvas();
    expect(
      screen.queryByRole('button', { name: 'Workflow' })
    ).not.toBeInTheDocument();
  });
});

// parity:run-tab-spec-edit-retry - the retry preview draws the run state of
// each step and follows the step that the review shows.
describe('DetailsCanvas with a host overlay and selection', () => {
  const overlay: RunOverlay = {
    say: {
      status: NodeStatus.Skipped,
      statusLabel: 'Reuse previous output',
      retryCount: 0,
      doneCount: 0,
    },
    shout: {
      status: NodeStatus.NotStarted,
      statusLabel: 'Run again',
      retryCount: 0,
      doneCount: 0,
    },
  };

  const status = (step: string) =>
    screen
      .getByTestId(`ve-canvas-node-${step}`)
      .querySelector('[data-testid="ve-canvas-node-status"]');

  it('shows no run state without an overlay', () => {
    renderCanvas();
    expect(screen.queryByTestId('ve-canvas-node-status')).toBeNull();
  });

  it('shows the run state of each step from the overlay', () => {
    renderCanvas({ overlay });

    expect(status('say')).toHaveAttribute(
      'data-status',
      String(NodeStatus.Skipped)
    );
    expect(
      within(screen.getByTestId('ve-canvas-node-say')).getByRole('img', {
        name: 'Reuse previous output',
      })
    ).toHaveAttribute('title', 'Reuse previous output');
    expect(status('shout')).toHaveAttribute(
      'data-status',
      String(NodeStatus.NotStarted)
    );
  });

  it('marks the step that the caller selects and reports clicks', () => {
    const { onSelectStep } = renderCanvas({ selectedStepName: 'say' });

    const ring = (step: string) =>
      screen.getByTestId(`ve-canvas-node-${step}`).classList;
    expect(ring('say')).toContain('ring-primary');
    expect(ring('shout')).not.toContain('ring-primary');

    fireEvent.click(screen.getByTestId('ve-canvas-node-shout'));
    expect(onSelectStep).toHaveBeenLastCalledWith('shout');
    // The caller did not change its selection, so the mark stays.
    expect(ring('say')).toContain('ring-primary');
    expect(ring('shout')).not.toContain('ring-primary');
  });
});
