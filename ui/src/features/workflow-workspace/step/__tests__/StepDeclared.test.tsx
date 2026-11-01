// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { WorkspaceDataContext } from '../../data/context';
import type { DAGPageData, WorkspaceRunValue } from '../../data/types';
import { StepDeclared } from '../StepDeclared';

const dag = {
  name: 'example',
  steps: [
    { name: 'fetch', output: 'BODY' },
    {
      name: 'build',
      id: 'b',
      outputs: [{ name: 'image', type: 'string' }],
    },
    { name: 'plain' },
  ],
};

function renderDeclared(
  step: string,
  runId: string | null,
  path = '/dags/example.yaml?remoteNode=edge',
  latestRunId: string | null = runId
) {
  const run = {
    ref: runId ? { name: 'example', runId } : null,
    latestRef: latestRunId
      ? { runId: latestRunId, status: Status.Success }
      : null,
  } as WorkspaceRunValue;
  return render(
    <MemoryRouter initialEntries={[path]}>
      <WorkspaceDataContext.Provider
        value={{
          fileName: 'example.yaml',
          dagData: { dag } as unknown as DAGPageData,
          workspaceName: null,
          remoteNode: 'edge',
          run,
          refresh: vi.fn(),
          registerHistory: () => () => undefined,
        }}
      >
        <Routes>
          <Route
            path="/dags/:fileName/:tab?"
            element={<StepDeclared step={step} node={undefined} />}
          />
        </Routes>
      </WorkspaceDataContext.Provider>
    </MemoryRouter>
  );
}

describe('StepDeclared', () => {
  it('lists the output variable of the definition without a run', () => {
    renderDeclared('fetch', null);
    expect(screen.getByText('${BODY}')).toBeInTheDocument();
    expect(screen.queryByRole('link')).toBeNull();
  });

  it('lists the named outputs with the id of the step', () => {
    renderDeclared('build', null);
    expect(screen.getByText('${steps.b.outputs.image}')).toBeInTheDocument();
    expect(screen.getByText('string')).toBeInTheDocument();
  });

  it('shows nothing for a step without outputs and without a run', () => {
    renderDeclared('plain', null);
    expect(screen.queryByTestId('ww-step-declared')).toBeNull();
  });

  it('links the run in the same tab only when the host gives a run id', () => {
    renderDeclared('plain', 'run-7');
    const link = screen.getByRole('link', {
      name: 'Open last run',
    });
    const href = link.getAttribute('href') ?? '';
    expect(href).toContain('/dags/example.yaml?');
    expect(href).not.toContain('/executions');
    expect(href).toContain('run=run-7');
    expect(href).toContain('step=plain');
    expect(href).toContain('remoteNode=edge');
  });

  it('links the last run while an older run is selected', () => {
    renderDeclared('plain', 'old-run', undefined, 'run-7');
    const href =
      screen
        .getByRole('link', { name: 'Open last run' })
        .getAttribute('href') ?? '';
    expect(href).toContain('run=run-7');
    expect(href).not.toContain('old-run');
  });

  it('links the run of the host when the last run is not known', () => {
    renderDeclared('plain', 'run-7', undefined, null);
    expect(
      screen.getByRole('link', { name: 'Open last run' }).getAttribute('href')
    ).toContain('run=run-7');
  });
});
