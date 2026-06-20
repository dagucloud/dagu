// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import DAGDetailsContent from '../DAGDetailsContent';

vi.mock('../..', () => ({
  DAGStatus: vi.fn(({ fillHeight }) => (
    <div data-fill-height={String(fillHeight)} data-testid="dag-status" />
  )),
}));

vi.mock('../', () => ({
  DAGHeader: () => null,
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
  DAGExecutionHistory: () => null,
  ExecutionLog: () => null,
  LogViewer: () => null,
  StepLog: () => null,
}));

vi.mock('../DAGSettingsTab', () => ({
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

function renderContent() {
  render(
    <MemoryRouter>
      <DAGDetailsContent
        fileName="release-notes"
        dag={dag}
        currentDAGRun={currentDAGRun}
        refreshFn={vi.fn()}
        formatDuration={vi.fn()}
        activeTab="status"
        isModal
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
});
