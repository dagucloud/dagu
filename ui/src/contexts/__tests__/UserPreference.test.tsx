// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  UserPreferencesProvider,
  useUserPreferences,
  type WorkflowFilterViewPreferences,
} from '../UserPreference';

const savedViews: WorkflowFilterViewPreferences = {
  local: {
    views: [
      {
        id: 'production',
        name: 'Production operations',
        filters: {
          searchText: 'deploy',
          searchLabels: ['env=prod'],
          sortField: 'name',
          sortOrder: 'asc',
        },
      },
    ],
    defaultViewId: 'production',
  },
};

function PreferenceProbe() {
  const { preferences, updatePreference } = useUserPreferences();

  return (
    <>
      <output data-testid="workflow-filter-views">
        {JSON.stringify(preferences.workflowFilterViews)}
      </output>
      <button
        type="button"
        onClick={() => updatePreference('workflowFilterViews', savedViews)}
      >
        Save workflow views
      </button>
    </>
  );
}

function renderPreferences() {
  return render(
    <UserPreferencesProvider>
      <PreferenceProbe />
    </UserPreferencesProvider>
  );
}

describe('UserPreferencesProvider', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    cleanup();
  });

  it('adds workflow view defaults when loading legacy preferences', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ pageLimit: 25 }));

    renderPreferences();

    expect(screen.getByTestId('workflow-filter-views')).toHaveTextContent('{}');
  });

  it('persists scoped workflow views', () => {
    renderPreferences();

    fireEvent.click(
      screen.getByRole('button', { name: 'Save workflow views' })
    );

    expect(screen.getByTestId('workflow-filter-views')).toHaveTextContent(
      JSON.stringify(savedViews)
    );
    expect(
      JSON.parse(localStorage.getItem('user_preferences') ?? '{}')
        .workflowFilterViews
    ).toEqual(savedViews);
  });
});
