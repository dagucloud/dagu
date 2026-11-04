// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import {
  WORKFLOW_PANEL_OPEN_KEY,
  YAML_PANEL_OPEN_KEY,
} from '../canvas/useYamlPanelMemory';
import { DOCK_HEIGHT_KEY, DOCK_OPEN_KEY } from '../dock/DockContext';
import { WorkspaceLoading } from '../WorkspaceLoading';

// The width of the right panels that the user left (StepSideDrawer).
const PANEL_WIDTH_KEY = 'dagu.stepDetailsDrawer.width';

function renderAt(path: string, url?: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <WorkspaceLoading fileName="graph-etl" url={url} />
    </MemoryRouter>
  );
}

// Until the data is there, the frame of the page shows with each part in its
// place: nothing moves when the page fills it in.
describe('WorkspaceLoading', () => {
  afterEach(() => localStorage.clear());

  it('shows the name in a bar of the height of the real bar', () => {
    renderAt('/dags/graph-etl');
    const name = screen.getByRole('heading', { name: 'graph-etl' });
    expect(name).toHaveClass('text-base', 'font-semibold');
    expect(name.parentElement).toHaveClass('h-10', 'border-b', 'px-3');
  });

  it('fills its area, with no padding of its own', () => {
    renderAt('/dags/graph-etl');
    const frame = screen.getByRole('status', { name: 'Loading...' });
    expect(frame).toHaveClass('h-full', 'w-full');
    expect(frame).not.toHaveClass('p-4');
  });

  it('shows the YAML panel that the page restores, at its stored width', () => {
    localStorage.setItem(YAML_PANEL_OPEN_KEY, 'true');
    localStorage.setItem(PANEL_WIDTH_KEY, '640');
    localStorage.setItem(DOCK_OPEN_KEY, 'true');
    renderAt('/dags/graph-etl');
    expect(screen.getByTestId('ww-loading-yaml')).toHaveStyle({
      width: '640px',
    });
    // The Run panel yields to it.
    expect(screen.queryByTestId('ww-loading-dock')).toBeNull();
  });

  it('shows the Workflow panel that the page restores', () => {
    localStorage.setItem(WORKFLOW_PANEL_OPEN_KEY, 'true');
    renderAt('/dags/graph-etl');
    expect(screen.getByTestId('ww-loading-workflow')).toBeInTheDocument();
    expect(screen.queryByTestId('ww-loading-yaml')).toBeNull();
  });

  it('shows no YAML panel when a link opens a step', () => {
    localStorage.setItem(YAML_PANEL_OPEN_KEY, 'true');
    renderAt('/dags/graph-etl?step=extract');
    expect(screen.queryByTestId('ww-loading-yaml')).toBeNull();
  });

  it('shows the Run panel that the page opens, at its stored height', () => {
    localStorage.setItem(DOCK_OPEN_KEY, 'true');
    localStorage.setItem(DOCK_HEIGHT_KEY, '320');
    renderAt('/dags/graph-etl');
    expect(screen.getByTestId('ww-loading-dock')).toHaveStyle({
      height: '320px',
    });
  });

  // A run row opens its workflow over the list: the run link opens the Run
  // panel, as on the page.
  it('reads the address of the page that is about to show', () => {
    renderAt('/dags', '/dags/graph-etl?run=r1');
    expect(screen.getByTestId('ww-loading-dock')).toBeInTheDocument();
  });
});
