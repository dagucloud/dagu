// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import * as React from 'react';
import { describe, expect, it } from 'vitest';

import { ControllerBuilder } from '../components/ControllerBuilder';
import { createControllerDraft } from '../draft';
import type { ControllerDefinition } from '../types';

function BuilderHarness() {
  const [definition, setDefinition] = React.useState<ControllerDefinition>(
    () => {
      const draft = createControllerDraft('ops');
      draft.name = 'Incident router';
      draft.description = 'Route incidents.';
      draft.llm.model = 'gpt-5';
      return draft;
    }
  );
  return (
    <ControllerBuilder
      definition={definition}
      workspace="ops"
      availableDAGs={[
        {
          fileName: 'triage',
          name: 'Triage',
          description: 'Classify the incident.',
          params: [],
        },
      ]}
      onChange={setDefinition}
    />
  );
}

describe('ControllerBuilder', () => {
  it('keeps the DAG text field synchronized with checkbox changes', async () => {
    const user = userEvent.setup();
    render(<BuilderHarness />);

    const dagList = screen.getByRole('textbox', {
      name: 'Controller DAG allowlist',
    });
    expect(dagList).toHaveValue('');

    await user.click(screen.getByRole('checkbox', { name: /triage/i }));

    await waitFor(() => expect(dagList).toHaveValue('triage'));
  });
});
