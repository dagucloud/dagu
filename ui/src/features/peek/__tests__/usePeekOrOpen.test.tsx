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

vi.mock('../WorkflowPeek', async () => {
  const { DialogTitle } = await import('@/components/ui/dialog');
  return {
    WorkflowPeek: ({ target }: { target: PeekTarget }) => (
      <DialogTitle>{target.dag}</DialogTitle>
    ),
  };
});

function LocationProbe() {
  const location = useLocation();
  return (
    <output data-testid="location">{`${location.pathname}${location.search}`}</output>
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
        {host ? <PeekHost>{children}</PeekHost> : children}
        <LocationProbe />
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

const location = () => screen.getByTestId('location').textContent;

describe('usePeekOrOpen', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shows a plain click in the peek over the list', () => {
    renderList(<Row target={{ dag: 'etl', run: 'run-1' }} />);

    fireEvent.click(screen.getByRole('row'));

    expect(location()).toBe('/dags?view=all&peek=etl&peekRun=run-1');
    expect(screen.getByRole('dialog', { name: 'etl' })).toBeInTheDocument();
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
  });

  it('opens the full page on a narrow viewport', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(600);
    renderList(<Row target={{ dag: 'etl' }} />);

    fireEvent.click(screen.getByRole('row'));

    expect(location()).toBe('/dags/etl?remoteNode=local');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('opens the full page of a name that no peek can read', () => {
    renderList(<Row target={{ dag: 'my dag' }} />);

    fireEvent.click(screen.getByRole('row'));

    expect(location()).toBe('/dags/my%20dag?remoteNode=local');
  });
});

describe('PeekLink', () => {
  it('links the full page and shows the peek on a plain click', () => {
    renderList(<PeekLink peek={{ dag: 'etl', run: 'run-1' }}>etl</PeekLink>);
    const link = screen.getByRole('link', { name: 'etl' });

    expect(link).toHaveAttribute(
      'href',
      '/dags/etl?remoteNode=local&run=run-1'
    );
    fireEvent.click(link);

    expect(location()).toBe('/dags?view=all&peek=etl&peekRun=run-1');
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
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
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
