// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { NodeStatus, Stream } from '@/api/v1/schema';
import DAGDetailsContent from '../DAGDetailsContent';

vi.mock('../..', () => ({
  DAGStatus: vi.fn(
    ({
      fillHeight,
      dagRun,
      afterGraph,
    }: {
      fillHeight?: boolean;
      dagRun?: { dagRunId: string };
      afterGraph?: React.ReactNode;
    }) => (
      <div
        data-fill-height={String(fillHeight)}
        data-run-id={dagRun?.dagRunId}
        data-testid="dag-status"
      >
        {afterGraph}
      </div>
    )
  ),
}));

vi.mock('../', () => ({
  DAGHeader: ({ currentDAGRun }: { currentDAGRun?: { dagRunId: string } }) => (
    <div data-run-id={currentDAGRun?.dagRunId} data-testid="dag-header" />
  ),
}));

const history = vi.hoisted(() => ({
  dagRuns: [] as { dagRunId: string }[],
}));

vi.mock('../../../hooks/useDAGRunHistory', () => ({
  useDAGRunHistory: () => ({
    dagRuns: history.dagRuns,
    gridData: [],
    refresh: vi.fn(),
  }),
}));

vi.mock('../../common', () => ({
  LinkTab: ({ label }: { label: string }) => <span>{label}</span>,
}));

vi.mock('../../common/ModalLinkTab', () => ({
  default: ({ label }: { label: string }) => <button>{label}</button>,
}));

vi.mock('../../dag-editor', () => ({
  DAGEditButtons: () => null,
  DAGSpec: () => null,
}));

vi.mock('../../dag-execution', () => ({
  // Picks a run by its ID, or the latest run with null.
  RunHistoryGrid: ({ onSelect }: { onSelect: (id: string | null) => void }) => (
    <>
      <button onClick={() => onSelect('older')}>pick older</button>
      <button onClick={() => onSelect(null)}>pick latest</button>
    </>
  ),
  ExecutionLog: () => null,
  LogViewer: () => null,
  StepLog: ({
    stream,
    node,
  }: {
    stream?: string;
    node?: { status?: number };
  }) => (
    <div
      data-testid="step-log"
      data-stream={stream}
      data-node-status={node?.status}
    />
  ),
}));

vi.mock('../DAGSettingsTab', () => ({
  default: () => null,
}));

vi.mock('../DAGWikiTab', () => ({
  default: () => null,
}));

vi.mock('../IncidentsTab', () => ({
  default: () => null,
}));

vi.mock('../NotificationsTab', () => ({
  default: () => null,
}));

vi.mock('../WebhookTab', () => ({
  default: () => null,
}));

const optionalTabs = vi.hoisted(() => ({ settings: true }));

vi.mock('../useOptionalDAGTabs', () => ({
  useOptionalDAGTabs: () => optionalTabs,
}));

const dag = {
  name: 'release-notes',
  artifacts: {
    enabled: true,
  },
} as never;

const currentDAGRun = {
  name: 'release-notes',
  dagRunId: 'run-1',
  artifactsAvailable: true,
} as never;

function renderContent(
  activeTab = 'status',
  isModal = true,
  onPickedRunChange?: (run: unknown) => void,
  initialEntry = '/'
) {
  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <DAGDetailsContent
        fileName="release-notes"
        dag={dag}
        currentDAGRun={currentDAGRun}
        refreshFn={vi.fn()}
        activeTab={activeTab}
        isModal={isModal}
        onPickedRunChange={onPickedRunChange}
        fillHeight
      />
    </MemoryRouter>
  );
}

describe('DAGDetailsContent', () => {
  it('passes full-height layout through to the latest-run status tabs', () => {
    renderContent();

    expect(screen.getByTestId('dag-status')).toHaveAttribute(
      'data-fill-height',
      'true'
    );
  });

  // One tab row serves every width, so each label appears once.
  it('shows each workflow tab once', () => {
    renderContent();

    for (const label of [
      'Overview',
      'Incidents',
      'Spec',
      'Webhook',
      'Settings',
      'Notifications',
      'Wiki',
    ]) {
      expect(screen.getAllByText(label)).toHaveLength(1);
    }
  });

  // The Webhook tab is the only place where a webhook is created, so it stays
  // listed. The Settings tab goes when there is no profile to choose.
  it('leaves out the Settings tab when the workflow has no profile to choose', () => {
    optionalTabs.settings = false;
    try {
      renderContent();

      expect(screen.getByText('Webhook')).toBeInTheDocument();
      expect(screen.queryByText('Settings')).not.toBeInTheDocument();
      expect(screen.getByText('Notifications')).toBeInTheDocument();
    } finally {
      optionalTabs.settings = true;
    }
  });

  // The history lives in the overview, so it has no tab of its own.
  it('has no History tab', () => {
    renderContent();

    expect(screen.queryByText('History')).not.toBeInTheDocument();
  });

  it('shows the run that the run picker selects, then the latest run again', () => {
    history.dagRuns = [{ dagRunId: 'latest-run' }, { dagRunId: 'older' }];
    const onPickedRunChange = vi.fn();
    try {
      renderContent('status', true, onPickedRunChange);
      const status = screen.getByTestId('dag-status');
      const header = screen.getByTestId('dag-header');
      expect(status).toHaveAttribute('data-run-id', 'run-1');

      fireEvent.click(screen.getByText('pick older'));
      expect(status).toHaveAttribute('data-run-id', 'older');
      expect(header).toHaveAttribute('data-run-id', 'older');
      // A page that draws its own header learns which run was picked.
      expect(onPickedRunChange).toHaveBeenLastCalledWith({ dagRunId: 'older' });

      fireEvent.click(screen.getByText('pick latest'));
      expect(status).toHaveAttribute('data-run-id', 'run-1');
      expect(header).toHaveAttribute('data-run-id', 'run-1');
      expect(onPickedRunChange).toHaveBeenLastCalledWith(undefined);
    } finally {
      history.dagRuns = [];
    }
  });

  // On the DAG page the picked run is in the URL, so a reload or a shared link
  // shows it. In a panel over the DAG list the query string is the list's, so
  // the panel keeps the run in its own state and leaves the URL alone.
  it('shows the run that the URL names on the DAG page', () => {
    history.dagRuns = [{ dagRunId: 'latest-run' }, { dagRunId: 'older' }];
    try {
      renderContent('status', false, undefined, '/?run=older');

      expect(screen.getByTestId('dag-status')).toHaveAttribute('data-run-id', 'older');
    } finally {
      history.dagRuns = [];
    }
  });

  it('does not read a run from the URL in a panel', () => {
    history.dagRuns = [{ dagRunId: 'latest-run' }, { dagRunId: 'older' }];
    try {
      renderContent('status', true, undefined, '/?run=older');

      expect(screen.getByTestId('dag-status')).toHaveAttribute('data-run-id', 'run-1');
    } finally {
      history.dagRuns = [];
    }
  });

  it.each(['log', 'dagRun-log'])(
    'adds a Log tab while %s is open',
    (activeTab) => {
      renderContent(activeTab);

      expect(screen.getAllByText('Log')).toHaveLength(1);
    }
  );

  it('opens the full-page step log on the linked stream with live status', () => {
    render(
      <MemoryRouter>
        <DAGDetailsContent
          fileName="release-notes"
          dag={dag}
          currentDAGRun={
            {
              ...(currentDAGRun as object),
              nodes: [{ step: { name: 'build' }, status: NodeStatus.Running }],
            } as never
          }
          refreshFn={vi.fn()}
          activeTab="log"
          stepName="build"
          logStream={Stream.stderr}
          fillHeight
        />
      </MemoryRouter>
    );

    const stepLog = screen.getByTestId('step-log');
    expect(stepLog).toHaveAttribute('data-stream', Stream.stderr);
    expect(stepLog).toHaveAttribute(
      'data-node-status',
      String(NodeStatus.Running)
    );
  });

  it('does not link the modal-only Wiki tab from the DAG page', () => {
    renderContent('status', false);
    expect(screen.queryByText('Wiki')).not.toBeInTheDocument();
  });
});
