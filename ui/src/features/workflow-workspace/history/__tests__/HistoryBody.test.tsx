// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { run } from '../../data/__tests__/fixtures';
import { HistoryBody } from '../HistoryBody';

const h = vi.hoisted(() => ({
  setUrl: vi.fn(),
  loadMore: vi.fn(),
  older: 'r1' as string | null,
}));

vi.mock('../../data/useVisibleRuns', () => ({
  useHistoryRuns: () => ({
    runs: [run('r1'), run('r2')],
    isLoading: false,
    selectedId: 'r2',
    older: {
      runs: [],
      hasMore: true,
      loading: false,
      failed: false,
      loadMore: h.loadMore,
    },
    adjacentId: (dir: 'older' | 'newer') => (dir === 'older' ? h.older : null),
  }),
}));
vi.mock('../../state/workspaceUrl', () => ({
  useWorkspaceUrl: () => [{}, h.setUrl],
}));
vi.mock('../../dock/DockContext', () => ({
  useDockCommands: () => ({ openStep: vi.fn(), dropAutoStep: vi.fn() }),
}));
vi.mock('../../canvas/WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => ({ handle: null }),
}));
vi.mock('../StepsGrid', () => ({
  StepsGrid: () => <div data-testid="grid" />,
}));
vi.mock('../../continuous/SamplesPanel', () => ({
  SamplesPanel: (props: { testId: string; source: unknown }) => (
    <div data-testid={props.testId}>{JSON.stringify(props.source)}</div>
  ),
}));

function renderBody(dag: object = { name: 'demo', steps: [] }) {
  render(
    <>
      <button type="button" id="ww-dock-tab-history">
        History
      </button>
      <button type="button">Elsewhere</button>
      <HistoryBody
        run={{ details: null, nodeByStep: new Map() } as never}
        fileName="demo.yaml"
        dag={dag as never}
      />
    </>
  );
}

// Left and Right on the focused History tab of the Activity panel go
// through the runs, as the arrows of the run chip do.
// parity:test-history-tab-untested
describe('HistoryBody on the focused History tab', () => {
  beforeEach(() => {
    h.setUrl.mockReset();
    h.loadMore.mockReset();
    h.older = 'r1';
  });

  it('selects the older run with Left', () => {
    renderBody();
    const tab = screen.getByRole('button', { name: 'History' });
    tab.focus();
    fireEvent.keyDown(tab, { key: 'ArrowLeft' });
    expect(h.setUrl).toHaveBeenCalledWith({ run: 'r1' });
  });

  // The oldest run that shows is not the oldest one on the server.
  it('reads older runs with Left on the oldest run', () => {
    h.older = null;
    renderBody();
    fireEvent.keyDown(screen.getByRole('button', { name: 'History' }), {
      key: 'ArrowLeft',
    });
    expect(h.loadMore).toHaveBeenCalledTimes(1);
    expect(h.setUrl).not.toHaveBeenCalled();
  });

  // Shift with the arrows still moves between the tabs.
  it('leaves Shift with the arrows to the tab strip', () => {
    renderBody();
    fireEvent.keyDown(screen.getByRole('button', { name: 'History' }), {
      key: 'ArrowLeft',
      shiftKey: true,
    });
    expect(h.setUrl).not.toHaveBeenCalled();
  });

  it('does nothing at the end of the runs or away from the tab', () => {
    renderBody();
    fireEvent.keyDown(screen.getByRole('button', { name: 'History' }), {
      key: 'ArrowRight',
    });
    fireEvent.keyDown(screen.getByRole('button', { name: 'Elsewhere' }), {
      key: 'ArrowLeft',
    });
    expect(h.setUrl).not.toHaveBeenCalled();
  });
});

// The history of a service DAG always shows above its runs.
describe('HistoryBody of a service DAG', () => {
  const service = { name: 'demo', steps: [], restartPolicy: 'always' };

  it('shows the run history above the grid', () => {
    renderBody(service);
    const panel = screen.getByTestId('ww-history-service');
    expect(panel).toHaveTextContent(
      '{"kind":"run-history","fileName":"demo.yaml"}'
    );
    expect(
      panel.compareDocumentPosition(screen.getByTestId('grid')) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('adds nothing for another DAG', () => {
    renderBody();
    expect(screen.queryByTestId('ww-history-service')).toBeNull();
  });
});
