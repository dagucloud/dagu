// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
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

    const input = screen.getByLabelText('AWS region');
    expect(screen.getByText('region')).toBeInTheDocument();
    // The description is read before the value is entered.
    const description = screen.getByText(
      'Region the instances are launched in.'
    );
    expect(
      description.compareDocumentPosition(input) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('keeps the name of a titled boolean visible', () => {
    renderSchemaParams({
      dry_run: { type: 'boolean', title: 'Dry run' },
    });

    expect(
      screen.getByRole('checkbox', { name: 'Dry run' })
    ).toBeInTheDocument();
    expect(screen.getByText('dry_run')).toBeInTheDocument();
  });
});
