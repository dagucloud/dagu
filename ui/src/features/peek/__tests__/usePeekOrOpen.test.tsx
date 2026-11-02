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
import { usePeekOrOpen, usePeekSequence, usePeekShown } from '../usePeekOrOpen';
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
      <output data-testid="can-step">{String(!!overlay?.canStep(1))}</output>
      <button type="button" onClick={() => overlay?.close()}>
        Close overlay
      </button>
    </>
  );
}

/** A row of a list; aria-current marks the row that the overlay shows. */
function Row({ target }: { target: PeekTarget }) {
  const peekOrOpen = usePeekOrOpen();
  const isShown = usePeekShown();
  return (
    <div
      role="row"
      aria-current={isShown(target) || undefined}
      onClick={(event) => peekOrOpen(target, event)}
    >
      {target.dag}
    </div>
  );
}

function Sequence({ targets }: { targets: PeekTarget[] }) {
  usePeekSequence(targets);
  return null;
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

  // The list shows where the arrow keys of the overlay ended, after it
  // closed too.
  it('marks the row that the overlay shows, and keeps it after close', () => {
    renderList(
      <>
        <Row target={{ dag: 'alpha' }} />
        <Row target={{ dag: 'beta' }} />
      </>
    );
    const row = (name: string) => screen.getByRole('row', { name });

    fireEvent.click(row('beta'));
    expect(row('beta')).toHaveAttribute('aria-current', 'true');
    expect(row('alpha')).not.toHaveAttribute('aria-current');

    fireEvent.click(screen.getByRole('button', { name: 'Close overlay' }));
    expect(overlay()).toBe('none');
    expect(row('beta')).toHaveAttribute('aria-current', 'true');
  });

  // The rows of a page that is gone do not drive the arrow keys of an
  // overlay that another page opens.
  it('forgets the rows of a list that unmounts', () => {
    const rows = [{ dag: 'alpha' }, { dag: 'beta' }];
    const view = renderList(
      <>
        <Sequence targets={rows} />
        <Row target={{ dag: 'alpha' }} />
      </>
    );
    view.rerender(
      <MemoryRouter initialEntries={['/dags?view=all']}>
        <ConfigContext.Provider value={{ basePath: '/dagu' } as Config}>
          <WorkflowOverlayProvider>
            <PeekHost>
              <Row target={{ dag: 'alpha' }} />
            </PeekHost>
            <LocationProbe />
          </WorkflowOverlayProvider>
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('row', { name: 'alpha' }));

    expect(overlay()).toBe('over /dags?view=all');
    expect(screen.getByTestId('can-step')).toHaveTextContent('false');
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
