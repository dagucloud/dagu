// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import StartDAGModal from '../StartDAGModal';

// Renders the real schema form, unlike StartDAGModal.test.tsx, so the
// parameter layout is checked as the dialog shows it.
vi.mock('@/contexts/AuthContext', () => ({
  useIsAdmin: () => true,
}));

function renderSchemaParams(properties: Record<string, unknown>) {
  render(
    <StartDAGModal
      visible={true}
      dismissModal={vi.fn()}
      onSubmit={vi.fn()}
      dag={
        {
          name: 'schema-dag',
          paramSchema: { type: 'object', properties },
        } as never
      }
    />
  );
}

describe('StartDAGModal schema parameters', () => {
  it('labels a titled field by its title, name beside it, description first', () => {
    renderSchemaParams({
      region: {
        type: 'string',
        title: 'AWS region',
        description: 'Region the instances are launched in.',
      },
    });

    // The name joins the accessible name, so params sharing a title differ.
    const input = screen.getByRole('textbox', { name: 'AWS region region' });
    // The description is read before the value is entered.
    const description = screen.getByText(
      'Region the instances are launched in.'
    );
    expect(
      description.compareDocumentPosition(input) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('names a radio group by its label', () => {
    renderSchemaParams({
      region: {
        type: 'string',
        title: 'AWS region',
        enum: ['us-east-1', 'us-west-2'],
      },
    });

    expect(
      screen.getByRole('radiogroup', { name: 'AWS region region' })
    ).toBeInTheDocument();
  });

  it('lets a double-click select a param name for copying', () => {
    renderSchemaParams({
      region: { type: 'string', title: 'AWS region' },
      notes: { type: 'string' },
    });

    // A cancelled mousedown with detail > 1 blocks the word selection.
    for (const name of ['region', 'notes']) {
      expect(fireEvent.mouseDown(screen.getByText(name), { detail: 2 })).toBe(
        true
      );
    }
  });

  it('labels a titled boolean by its title, name, then description', () => {
    renderSchemaParams({
      dry_run: {
        type: 'boolean',
        title: 'Dry run',
        description: 'Log the actions without launching anything.',
      },
    });

    const checkbox = screen.getByRole('checkbox', { name: 'Dry run dry_run' });
    const description = screen.getByText(
      'Log the actions without launching anything.'
    );
    expect(
      screen.getByText('Dry run').compareDocumentPosition(description) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(checkbox).toHaveAccessibleDescription(
      'Log the actions without launching anything.'
    );
  });
});
