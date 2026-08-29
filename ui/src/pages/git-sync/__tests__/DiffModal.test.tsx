// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SyncStatus } from '@/api/v1/schema';
import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@/contexts/UserPreference', () => ({
  useUserPreferences: () => ({ preferences: { theme: 'light' } }),
}));

import { DiffModal } from '../DiffModal';

describe('DiffModal', () => {
  it('renders a size panel instead of the diff viewer for binary items', () => {
    render(
      <DiffModal
        open
        onOpenChange={() => {}}
        dagId="docs/.attachments/guides/x/logo.png"
        status={SyncStatus.modified}
        binary
        localSize={2048}
        remoteSize={1024}
      />
    );

    expect(screen.getByText(/binary file/i)).toBeInTheDocument();
    expect(screen.getByText('2,048 bytes')).toBeInTheDocument();
    expect(screen.getByText('1,024 bytes')).toBeInTheDocument();
  });

  it('renders the text diff viewer for non-binary items', () => {
    render(
      <DiffModal
        open
        onOpenChange={() => {}}
        dagId="docs/guides/x"
        status={SyncStatus.modified}
        localContent="local"
        remoteContent="remote"
      />
    );

    expect(screen.queryByText(/binary file/i)).not.toBeInTheDocument();
    expect(screen.getByText('local')).toBeInTheDocument();
  });

  it('shows remote deletion', () => {
    render(
      <DiffModal
        open
        onOpenChange={() => {}}
        dagId="scripts/run.sh"
        status={SyncStatus.conflict}
        localContent="echo local"
        remoteDeleted
        localExecutable
      />
    );

    expect(
      screen.getByText('The remote file was deleted.')
    ).toBeInTheDocument();
    expect(screen.getByText('Remote (deleted)')).toBeInTheDocument();
  });

  it('shows executable mode differences', () => {
    render(
      <DiffModal
        open
        onOpenChange={() => {}}
        dagId="scripts/run.sh"
        status={SyncStatus.modified}
        localContent="echo ok"
        remoteContent="echo ok"
        localExecutable
        remoteExecutable={false}
      />
    );

    expect(
      screen.getByText('Mode: remote regular, local executable')
    ).toBeInTheDocument();
  });
});
