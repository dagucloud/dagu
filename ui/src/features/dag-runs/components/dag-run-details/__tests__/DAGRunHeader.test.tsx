// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  createEvent,
  fireEvent,
  render,
  screen,
} from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel } from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import DAGRunHeader from '../DAGRunHeader';

vi.mock('@/lib/clipboard', () => ({
  copyText: vi.fn(() => Promise.resolve(true)),
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ basePath: '' }),
}));

vi.mock('../../common', () => ({
  DAGRunActions: () => <span data-testid="actions" />,
}));

vi.mock('@/components/ui/matrix-text', () => ({
  MatrixText: ({ text }: { text: string }) => text,
}));

const dagRun = {
  name: 'nightly',
  dagRunId: 'run-42',
  rootDAGRunName: 'nightly',
  rootDAGRunId: 'run-42',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  startedAt: '',
  finishedAt: '',
  autoRetryCount: 0,
};

describe('DAGRunHeader', () => {
  it('announces the copy that happened last', async () => {
    render(
      <MemoryRouter>
        <RemoteNodeProvider remoteNode="local">
          <DAGRunHeader dagRun={dagRun as never} refreshFn={vi.fn()} />
        </RemoteNodeProvider>
      </MemoryRouter>
    );

    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Copy link to this run' })
      );
    });
    expect(
      screen.getByText('Run link copied to clipboard')
    ).toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy run ID' }));
    });
    expect(screen.getByText('Run ID copied to clipboard')).toBeInTheDocument();
    expect(
      screen.queryByText('Run link copied to clipboard')
    ).not.toBeInTheDocument();
  });

  // The workflow page has no address for a sub-run: its link opens the root run.
  it('names the root run on the copy link of a sub-run', () => {
    render(
      <MemoryRouter>
        <RemoteNodeProvider remoteNode="local">
          <DAGRunHeader
            dagRun={{ ...dagRun, name: 'child', dagRunId: 'child-1' } as never}
            refreshFn={vi.fn()}
          />
        </RemoteNodeProvider>
      </MemoryRouter>
    );

    expect(
      screen.getByRole('button', { name: 'Copy link to the root run' })
    ).toBeInTheDocument();
  });

  // The run page has the same one-row header as the workflow page.
  it('puts the name, the status icon, the run facts and the actions on one row', () => {
    render(
      <MemoryRouter>
        <RemoteNodeProvider remoteNode="local">
          <DAGRunHeader dagRun={dagRun as never} refreshFn={vi.fn()} />
        </RemoteNodeProvider>
      </MemoryRouter>
    );

    const row = screen
      .getByRole('heading', { name: 'nightly' })
      .closest('div.flex-wrap')!;
    expect(row).toHaveClass('@3xl:flex-nowrap');
    expect(row).toContainElement(
      screen.getByRole('img', { name: 'Succeeded' })
    );
    expect(row).toContainElement(screen.getByTitle('Run ID'));
    // Stop and Retry follow the run ID.
    const actions = screen.getByTestId('actions');
    expect(actions.parentElement).toContainElement(screen.getByTitle('Run ID'));
  });

  it('names the status in text next to its icon', () => {
    render(
      <MemoryRouter>
        <RemoteNodeProvider remoteNode="local">
          <DAGRunHeader dagRun={dagRun as never} refreshFn={vi.fn()} />
        </RemoteNodeProvider>
      </MemoryRouter>
    );
    expect(screen.getByText('Succeeded')).toBeVisible();
  });

  it('keeps the workspace scope on the links out of the page', () => {
    render(
      <MemoryRouter initialEntries={['/dag-runs/nightly/run-42?workspace=ops']}>
        <RemoteNodeProvider remoteNode="edge">
          <DAGRunHeader
            dagRun={{ ...dagRun, sourceFileName: 'nightly.yaml' } as never}
            refreshFn={vi.fn()}
          />
        </RemoteNodeProvider>
      </MemoryRouter>
    );
    expect(
      screen.getByRole('link', { name: 'Show in workflow' })
    ).toHaveAttribute(
      'href',
      '/dags/nightly.yaml?remoteNode=edge&workspace=ops&run=run-42'
    );
    expect(
      screen.getByRole('link', { name: 'View workflow definition' })
    ).toHaveAttribute(
      'href',
      '/dags/nightly.yaml?remoteNode=edge&workspace=ops'
    );
  });

  // parity:runpage-header
  // parity:run-header-title-copy
  describe('Show in workflow', () => {
    function renderHeader(run: Record<string, unknown>) {
      render(
        <MemoryRouter>
          <RemoteNodeProvider remoteNode="edge">
            <DAGRunHeader dagRun={run as never} refreshFn={vi.fn()} />
            <Location />
          </RemoteNodeProvider>
        </MemoryRouter>
      );
    }

    function Location() {
      const { pathname, search } = useLocation();
      return <span data-testid="location">{pathname + search}</span>;
    }

    it('opens a root run in the executions of its workflow', () => {
      renderHeader({ ...dagRun, sourceFileName: 'nightly.yaml' });

      const definition = screen.getByRole('link', {
        name: 'View workflow definition',
      });
      const show = screen.getByRole('link', { name: 'Show in workflow' });
      expect(show).toHaveAttribute(
        'href',
        '/dags/nightly.yaml?remoteNode=edge&run=run-42'
      );
      // It sits next to the definition link.
      expect(definition.nextElementSibling).toBe(show);
    });

    it('leaves a modified click on the definition link to the browser', () => {
      renderHeader({ ...dagRun, sourceFileName: 'nightly.yaml' });
      const before = screen.getByTestId('location').textContent;

      const link = screen.getByRole('link', {
        name: 'View workflow definition',
      });
      const event = createEvent.click(link, { ctrlKey: true });
      fireEvent(link, event);

      expect(event.defaultPrevented).toBe(false);
      expect(screen.getByTestId('location')).toHaveTextContent(before ?? '');
    });

    // A modified click follows the href, so the href must hold the base path.
    it('puts the base path of the app in the links to the workflow', () => {
      render(
        <MemoryRouter
          basename="/dagu"
          initialEntries={['/dagu/dag-runs/nightly/run-42']}
        >
          <RemoteNodeProvider remoteNode="edge">
            <DAGRunHeader
              dagRun={{ ...dagRun, sourceFileName: 'nightly.yaml' } as never}
              refreshFn={vi.fn()}
            />
          </RemoteNodeProvider>
        </MemoryRouter>
      );

      expect(
        screen.getByRole('link', { name: 'View workflow definition' })
      ).toHaveAttribute('href', '/dagu/dags/nightly.yaml?remoteNode=edge');
      expect(
        screen.getByRole('link', { name: 'Show in workflow' })
      ).toHaveAttribute(
        'href',
        '/dagu/dags/nightly.yaml?remoteNode=edge&run=run-42'
      );
    });

    it('navigates inside the app on a plain click', () => {
      renderHeader({ ...dagRun, sourceFileName: 'nightly.yaml' });

      fireEvent.click(screen.getByRole('link', { name: 'Show in workflow' }));

      expect(screen.getByTestId('location')).toHaveTextContent(
        '/dags/nightly.yaml?remoteNode=edge&run=run-42'
      );
    });

    it('is absent for a sub-run', () => {
      renderHeader({
        ...dagRun,
        name: 'child',
        dagRunId: 'child-1',
        sourceFileName: 'child.yaml',
      });

      expect(
        screen.getByRole('link', { name: 'View workflow definition' })
      ).toBeInTheDocument();
      expect(
        screen.queryByRole('link', { name: 'Show in workflow' })
      ).not.toBeInTheDocument();
    });

    it('is absent without a source file', () => {
      renderHeader(dagRun);

      expect(
        screen.queryByRole('link', { name: 'Show in workflow' })
      ).not.toBeInTheDocument();
    });
  });
});
