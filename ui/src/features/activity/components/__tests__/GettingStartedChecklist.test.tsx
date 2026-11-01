// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { WorkspaceKind } from '@/lib/workspace';
import type { GettingStartedStep } from '../../hooks/useGettingStarted';
import { GettingStartedChecklist } from '../GettingStartedChecklist';

vi.mock('@/contexts/AuthContext', () => ({ useCanWrite: () => true }));
vi.mock('@/hooks/api', () => ({ useClient: () => ({ POST: vi.fn() }) }));

const freshSteps: GettingStartedStep[] = [
  { id: 'run', done: false, exampleFile: 'example-06-retry-on-failure' },
  { id: 'create', done: false },
  { id: 'schedule', done: false },
  { id: 'alert', done: false, canConnect: true, canAddChannel: true },
];

function renderChecklist(
  steps: GettingStartedStep[] = freshSteps,
  onDismiss = vi.fn()
) {
  render(
    <MemoryRouter>
      <AppBarContext.Provider
        value={
          {
            selectedRemoteNode: 'local',
            workspaceSelection: { kind: WorkspaceKind.all },
          } as never
        }
      >
        <GettingStartedChecklist steps={steps} onDismiss={onDismiss} />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
  return { onDismiss };
}

function stepItem(title: string): HTMLElement {
  return screen.getByText(title).closest('li')!;
}

describe('GettingStartedChecklist', () => {
  it('offers an action for each step left', () => {
    renderChecklist();

    const checklist = screen.getByRole('region', { name: 'Getting started' });
    expect(checklist).toHaveTextContent('0 of 4 done');
    expect(within(checklist).getAllByRole('listitem')).toHaveLength(4);
    expect(
      within(stepItem('Run a workflow')).getByRole('link', {
        name: 'Open the retry example',
      })
    ).toHaveAttribute('href', '/dags/example-06-retry-on-failure');
    expect(
      within(stepItem('Create your own workflow')).getByRole('button', {
        name: 'Create new DAG',
      })
    ).toBeVisible();
    expect(
      within(stepItem('Put a workflow on a schedule')).getByRole('link', {
        name: 'Scheduling guide',
      })
    ).toHaveAttribute(
      'href',
      'https://docs.dagu.sh/writing-workflows/scheduling'
    );
    const alert = within(stepItem('Get alerted when something fails'));
    expect(
      alert.getByRole('link', { name: 'Connect for free' })
    ).toHaveAttribute('href', '/license#monitoring');
    expect(
      alert.getByRole('link', { name: 'Add a notification channel' })
    ).toHaveAttribute('href', '/notification-channels');
  });

  it('points at the Workflows page without the retry example', () => {
    renderChecklist([{ id: 'run', done: false }]);

    expect(
      screen.getByRole('link', { name: 'Open Workflows' })
    ).toHaveAttribute('href', '/dags');
  });

  it('shrinks a done step to its title', () => {
    renderChecklist([
      { id: 'run', done: true, exampleFile: 'example-06-retry-on-failure' },
      { id: 'create', done: false },
    ]);

    expect(screen.getByText('1 of 2 done')).toBeVisible();
    const done = stepItem('Run a workflow');
    expect(done).toHaveTextContent('Run a workflow (Done)');
    expect(within(done).queryByRole('link')).not.toBeInTheDocument();
  });

  it('offers only the alert actions the user may take', () => {
    renderChecklist([
      { id: 'alert', done: false, canConnect: false, canAddChannel: true },
    ]);

    expect(
      screen.queryByRole('link', { name: 'Connect for free' })
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: 'Add a notification channel' })
    ).toBeVisible();
  });

  it('dismisses', () => {
    const { onDismiss } = renderChecklist();

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));

    expect(onDismiss).toHaveBeenCalledOnce();
  });
});
