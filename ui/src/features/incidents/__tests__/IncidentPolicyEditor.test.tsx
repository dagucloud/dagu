// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { IncidentPolicyScope } from '@/api/v1/schema';
import { IncidentPolicyEditor } from '../IncidentPolicyEditor';
import { policySetDraftFromAPI } from '../incidentDrafts';

describe('IncidentPolicyEditor', () => {
  it('does not show no-op enabled controls when inheriting parent policy', () => {
    const draft = policySetDraftFromAPI(
      {
        scope: IncidentPolicyScope.workspace,
        workspace: 'ops',
        enabled: false,
        inheritParent: true,
        policies: [],
      },
      IncidentPolicyScope.workspace,
      'ops'
    );

    render(
      <MemoryRouter>
        <IncidentPolicyEditor
          draft={draft}
          providers={[]}
          allowInherit
          inheritTitle="Workspace override"
          inheritDescription="Workspace settings can inherit Global."
          onChange={vi.fn()}
        />
      </MemoryRouter>
    );

    expect(screen.getByText('Inherits')).toBeVisible();
    expect(screen.queryByText(/^Enabled$/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Disabled$/)).not.toBeInTheDocument();
  });
});
