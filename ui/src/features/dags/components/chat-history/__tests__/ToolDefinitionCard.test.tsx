// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ToolDefinitionCard } from '../ToolDefinitionCard';

const parameters = {
  type: 'object',
  properties: { amount: { type: 'number' } },
  required: ['amount'],
};

describe('ToolDefinitionCard', () => {
  it('shows the source badge and the parameters of an offered tool', () => {
    const { getByTestId, queryByTestId, getByText } = render(
      <ToolDefinitionCard
        tool={{
          name: 'refund',
          description: 'Refund a customer',
          parameters,
          source: 'mcp:docs',
        }}
      />
    );
    expect(getByTestId('tool-source')).toHaveTextContent('mcp:docs');
    expect(queryByTestId('tool-omitted')).toBeNull();
    expect(getByText('amount')).toBeInTheDocument();
  });

  it('shows the skills source of the tool load_skill', () => {
    const { getByTestId } = render(
      <ToolDefinitionCard
        tool={{
          name: 'load_skill',
          description: 'Read the instructions of a skill',
          parameters: {
            type: 'object',
            properties: { name: { type: 'string', enum: ['triage'] } },
            required: ['name'],
          },
          source: 'skills',
        }}
      />
    );
    expect(getByTestId('tool-source')).toHaveTextContent('skills');
  });

  it('has no source badge when the server gives none', () => {
    const { queryByTestId } = render(
      <ToolDefinitionCard tool={{ name: 'refund', parameters }} />
    );
    expect(queryByTestId('tool-source')).toBeNull();
  });
});
