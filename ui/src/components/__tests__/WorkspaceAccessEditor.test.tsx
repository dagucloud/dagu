// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { UserRole } from '@/api/v1/schema';
import { WorkspaceAccessSummary } from '@/components/WorkspaceAccessEditor';

describe('WorkspaceAccessSummary', () => {
  it('shows an explicit empty scoped-access state', () => {
    render(
      <WorkspaceAccessSummary
        value={{ all: false, grants: [] }}
        workspaces={[]}
      />
    );

    expect(screen.getByText('No named workspace grants')).toBeVisible();
  });

  it('keeps unavailable workspace grants visible', () => {
    render(
      <WorkspaceAccessSummary
        value={{
          all: false,
          grants: [
            { workspace: 'future-workspace', role: UserRole.developer },
            { workspace: 'payments', role: UserRole.operator },
          ],
        }}
        workspaces={[{ id: 'payments', name: 'payments' }]}
      />
    );

    expect(screen.getByText('future-workspace')).toBeVisible();
    expect(screen.getByText('payments')).toBeVisible();
    expect(screen.getByText('Workspace not currently available')).toBeVisible();
  });
});
