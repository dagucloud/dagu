// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import StartDAGModal from '../StartDAGModal';

// Renders the real schema form, unlike StartDAGModal.test.tsx, so the
// parameter layout is checked as the dialog shows it.
vi.mock('@/contexts/AuthContext', () => ({
  useIsAdmin: () => true,
}));

// The slot gives the date param its value.
vi.mock('../SlotDatesPreview', () => ({
  SlotDatesPreview: ({
    onParams,
  }: {
    onParams: (values: Record<string, string>) => void;
  }) => {
    React.useEffect(() => onParams({ date: '2026-11-09' }), [onParams]);
    return null;
  },
}));

function renderSchemaParams(
  properties: Record<string, unknown>,
  required: string[] = []
) {
  render(
    <StartDAGModal
      visible={true}
      dismissModal={vi.fn()}
      onSubmit={vi.fn()}
      dag={
        {
          name: 'schema-dag',
          paramSchema: { type: 'object', properties, required },
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

  // A date field shows no placeholder, so the value the param takes from the
  // slot is told beside it.
  it('tells the value a date param takes from the slot', async () => {
    render(
      <StartDAGModal
        visible={true}
        fileName="etl"
        dismissModal={vi.fn()}
        onSubmit={vi.fn()}
        dag={
          {
            name: 'etl',
            dateNames: ['target_day'],
            paramSchema: {
              type: 'object',
              properties: { date: { type: 'string', format: 'date' } },
            },
          } as never
        }
      />
    );

    expect(
      await screen.findByText('Left empty, it takes 2026-11-09 from the slot.')
    ).toBeInTheDocument();
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

  it('marks a boolean that fails validation invalid', async () => {
    renderSchemaParams({ confirm: { type: 'boolean', title: 'Confirm' } }, [
      'confirm',
    ]);

    fireEvent.click(screen.getByRole('button', { name: 'Enqueue' }));

    await waitFor(() =>
      expect(
        screen.getByRole('checkbox', { name: 'Confirm confirm' })
      ).toHaveAttribute('aria-invalid', 'true')
    );
  });
});
