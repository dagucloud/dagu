// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { type Config, ConfigContext } from '@/contexts/ConfigContext';
import { PeekHost } from '../PeekHost';
import { PeekLink } from '../PeekLink';
import type { PeekTarget } from '../peekParams';
import { usePeekOrOpen } from '../usePeekOrOpen';
import {
  useWorkflowOverlay,
  WorkflowOverlayProvider,
} from '../WorkflowOverlay';

function LocationProbe() {
  const location = useLocation();
  const overlay = useWorkflowOverlay();
  const below = overlay?.background;
  return (
    <>
      <output data-testid="location">{`${location.pathname}${location.search}`}</output>
      <output data-testid="overlay">
        {below ? `over ${below.pathname}${below.search}` : 'none'}
      </output>
    </>
  );
}

function Row({ target }: { target: PeekTarget }) {
  const peekOrOpen = usePeekOrOpen();
  return (
    <div role="row" onClick={(event) => peekOrOpen(target, event)}>
      {target.dag}
    </div>
  );
}

function renderList(children: ReactNode, { host = true } = {}) {
  return render(
    <MemoryRouter initialEntries={['/dags?view=all']}>
      <ConfigContext.Provider value={{ basePath: '/dagu' } as Config}>
        <WorkflowOverlayProvider>
          {host ? <PeekHost>{children}</PeekHost> : children}
          <LocationProbe />
        </WorkflowOverlayProvider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

const location = () => screen.getByTestId('location').textContent;
const overlay = () => screen.getByTestId('overlay').textContent;

describe('usePeekOrOpen', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('opens the workflow page over the list on a plain click', () => {
    renderList(<Row target={{ dag: 'etl', run: 'run-1' }} />);

    fireEvent.click(screen.getByRole('row'));

    expect(location()).toBe('/dags/etl?remoteNode=local&run=run-1');
    expect(overlay()).toBe('over /dags?view=all');
  });

  it.each(['ctrlKey', 'metaKey'])(
    'opens the full page in a new tab with %s',
    (modifier) => {
      const open = vi.spyOn(window, 'open').mockImplementation(() => null);
      renderList(<Row target={{ dag: 'etl' }} />);

      fireEvent.click(screen.getByRole('row'), { [modifier]: true });

      expect(open).toHaveBeenCalledWith(
        '/dagu/dags/etl?remoteNode=local',
        '_blank',
        'noopener'
      );
      expect(location()).toBe('/dags?view=all');
    }
  );

  it('opens the full page without a PeekHost', () => {
    renderList(<Row target={{ dag: 'etl' }} />, { host: false });

    fireEvent.click(screen.getByRole('row'));

    expect(location()).toBe('/dags/etl?remoteNode=local');
    expect(overlay()).toBe('none');
  });

  it('opens the full page on a narrow viewport', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(600);
    renderList(<Row target={{ dag: 'etl' }} />);

    fireEvent.click(screen.getByRole('row'));

    expect(location()).toBe('/dags/etl?remoteNode=local');
    expect(overlay()).toBe('none');
  });

  it('opens the full page of a name that no peek can read', () => {
    renderList(<Row target={{ dag: 'my dag' }} />);

    fireEvent.click(screen.getByRole('row'));

    expect(location()).toBe('/dags/my%20dag?remoteNode=local');
  });
});

describe('PeekLink', () => {
  it('links the full page and opens it over the list on a plain click', () => {
    renderList(<PeekLink peek={{ dag: 'etl', run: 'run-1' }}>etl</PeekLink>);
    const link = screen.getByRole('link', { name: 'etl' });

    expect(link).toHaveAttribute(
      'href',
      '/dags/etl?remoteNode=local&run=run-1'
    );
    fireEvent.click(link);

    expect(location()).toBe('/dags/etl?remoteNode=local&run=run-1');
    expect(overlay()).toBe('over /dags?view=all');
  });

  it('keeps the native link for a modified click', () => {
    // jsdom cannot open the new tab that the browser would.
    const stopNavigation = (event: Event) => event.preventDefault();
    document.addEventListener('click', stopNavigation);
    renderList(<PeekLink peek={{ dag: 'etl' }}>etl</PeekLink>);

    fireEvent.click(screen.getByRole('link', { name: 'etl' }), {
      ctrlKey: true,
    });

    expect(location()).toBe('/dags?view=all');
    expect(overlay()).toBe('none');
    document.removeEventListener('click', stopNavigation);
  });

  it('follows the link without a PeekHost', () => {
    renderList(<PeekLink peek={{ dag: 'etl' }}>etl</PeekLink>, {
      host: false,
    });

    fireEvent.click(screen.getByRole('link', { name: 'etl' }));

    expect(location()).toBe('/dags/etl?remoteNode=local');
  });
});
