// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { PeekHost } from '../PeekHost';
import { usePeekOrOpen, usePeekSequence } from '../usePeekOrOpen';
import {
  useWorkflowOverlay,
  WorkflowOverlayProvider,
} from '../WorkflowOverlay';

/** The URL, the page below the overlay, and the controls of the overlay. */
function Probe() {
  const location = useLocation();
  const navigate = useNavigate();
  const overlay = useWorkflowOverlay();
  const peekOrOpen = usePeekOrOpen();
  usePeekSequence([{ dag: 'etl' }, { dag: 'load' }]);
  const below = overlay?.background;
  return (
    <>
      <output data-testid="location">{`${location.pathname}${location.search}`}</output>
      <output data-testid="overlay">
        {below ? `over ${below.pathname}${below.search}` : 'none'}
      </output>
      <button type="button" onClick={() => peekOrOpen({ dag: 'etl' })}>
        open etl
      </button>
      <button type="button" onClick={() => overlay?.close()}>
        close
      </button>
      <button type="button" onClick={() => overlay?.expand()}>
        expand
      </button>
      <button type="button" onClick={() => overlay?.step(1)}>
        next row
      </button>
      <output data-testid="can-step">
        {`${overlay?.canStep(-1)} ${overlay?.canStep(1)}`}
      </output>
      <button type="button" onClick={() => navigate('/dags/etl?run=r2')}>
        pick run
      </button>
      <button type="button" onClick={() => navigate(-1)}>
        back
      </button>
      <button type="button" onClick={() => navigate('/queues')}>
        queues
      </button>
    </>
  );
}

function renderList(url = '/dag-runs?status=failed') {
  render(
    <MemoryRouter initialEntries={[url]}>
      <WorkflowOverlayProvider>
        <PeekHost>
          <Probe />
        </PeekHost>
      </WorkflowOverlayProvider>
    </MemoryRouter>
  );
}

const location = () => screen.getByTestId('location').textContent;
const overlay = () => screen.getByTestId('overlay').textContent;
const press = (name: string) =>
  act(() => fireEvent.click(screen.getByRole('button', { name })));

describe('the workflow overlay', () => {
  it('keeps the list below the workflow page that it opens', () => {
    renderList();

    press('open etl');

    expect(location()).toBe('/dags/etl?remoteNode=local');
    expect(overlay()).toBe('over /dag-runs?status=failed');
  });

  // The workflow page moves within itself: the overlay stays, and close goes
  // back to the list, not to the previous state of the workflow page.
  it('stays over the list while the workflow page moves, and closes to it', () => {
    renderList();
    press('open etl');
    press('pick run');
    expect(overlay()).toBe('over /dag-runs?status=failed');

    press('close');

    expect(location()).toBe('/dag-runs?status=failed');
    expect(overlay()).toBe('none');
  });

  it('ends when Back reaches the list', () => {
    renderList();
    press('open etl');

    press('back');

    expect(location()).toBe('/dag-runs?status=failed');
    expect(overlay()).toBe('none');
  });

  it('ends when the workflow page links to another page', () => {
    renderList();
    press('open etl');

    press('queues');

    expect(location()).toBe('/queues');
    expect(overlay()).toBe('none');
  });

  it('keeps the workflow page as the page on expand', () => {
    renderList();
    press('open etl');

    press('expand');

    expect(location()).toBe('/dags/etl?remoteNode=local');
    expect(overlay()).toBe('none');
  });

  // Up and Down of the overlay step through the rows of the list; a step
  // takes the place of the shown row, so close still goes to the list.
  it('steps to the next row of the list and closes to the list', () => {
    renderList();
    press('open etl');
    expect(screen.getByTestId('can-step')).toHaveTextContent('false true');

    press('next row');

    expect(location()).toBe('/dags/load?remoteNode=local');
    expect(overlay()).toBe('over /dag-runs?status=failed');
    expect(screen.getByTestId('can-step')).toHaveTextContent('true false');
    press('next row');
    expect(location()).toBe('/dags/load?remoteNode=local');

    press('close');
    expect(location()).toBe('/dag-runs?status=failed');
  });

  // The version before had a preview at ?peek: its links open the workflow.
  it('opens the workflow page of a link with ?peek', () => {
    renderList('/dag-runs?peek=etl&peekRun=run-1');

    expect(location()).toBe('/dags/etl?remoteNode=local&run=run-1');
  });
});
