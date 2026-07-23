// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import * as React from 'react';
import { describe, expect, it, vi } from 'vitest';

import { ControllerBuilder } from '../components/ControllerBuilder';
import { createControllerDraft } from '../draft';
import type { ControllerDefinition } from '../types';

function BuilderHarness({
  controllerDAGs = [],
  configuredReview = false,
  dagOptionsError,
  onRetryDAGOptions,
}: {
  controllerDAGs?: string[];
  configuredReview?: boolean;
  dagOptionsError?: string;
  onRetryDAGOptions?: () => void;
}) {
  const [draftDirty, setDraftDirty] = React.useState(false);
  const [definition, setDefinition] = React.useState<ControllerDefinition>(
    () => {
      const draft = createControllerDraft('ops');
      draft.name = 'Incident router';
      draft.description = 'Route incidents.';
      draft.llm.model = 'gpt-5';
      draft.dags = controllerDAGs;
      draft.states.review = {
        description: 'Review the result.',
        dags: configuredReview ? ['triage'] : [],
        transitions: [],
        terminal: configuredReview ? undefined : 'succeeded',
      };
      return draft;
    }
  );
  return (
    <>
      <output aria-label="Buffered draft state">
        {draftDirty ? 'dirty' : 'clean'}
      </output>
      <ControllerBuilder
        definition={definition}
        workspace="ops"
        availableDAGs={[
          {
            fileName: 'triage',
            name: 'Triage',
            description: 'Classify the incident.',
          },
        ]}
        availableDAGsError={dagOptionsError}
        onRetryAvailableDAGs={onRetryDAGOptions}
        onDraftDirtyChange={setDraftDirty}
        onChange={setDefinition}
      />
    </>
  );
}

describe('ControllerBuilder', () => {
  it('reports buffered definition edits before blur', async () => {
    const user = userEvent.setup();
    render(<BuilderHarness />);

    const labels = screen.getByRole('textbox', {
      name: 'Controller labels',
    });
    await user.type(labels, 'urgent');

    expect(screen.getByLabelText('Buffered draft state')).toHaveTextContent(
      'dirty'
    );

    await user.tab();

    expect(screen.getByLabelText('Buffered draft state')).toHaveTextContent(
      'clean'
    );
  });

  it('commits comma-separated labels after editing finishes', async () => {
    const user = userEvent.setup();
    render(<BuilderHarness />);

    const labels = screen.getByRole('textbox', {
      name: 'Controller labels',
    });
    await user.type(labels, 'urgent, customer');

    expect(labels).toHaveValue('urgent, customer');

    await user.tab();

    expect(labels).toHaveValue('urgent, customer');
  });

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

  it('preserves an omitted system prompt during unrelated edits', async () => {
    const user = userEvent.setup();
    const definition = createControllerDraft('ops');
    delete definition.llm.system;
    const onChange = vi.fn();

    render(
      <ControllerBuilder
        definition={definition}
        workspace="ops"
        onChange={onChange}
      />
    );

    await user.click(screen.getByRole('button', { name: 'Add state' }));

    expect(onChange).toHaveBeenCalledOnce();
    expect(onChange.mock.calls[0]?.[0].llm).not.toHaveProperty('system');
  });

  it('restores a rejected state name edit', async () => {
    const user = userEvent.setup();
    render(<BuilderHarness />);

    const stateName = screen.getByRole('textbox', {
      name: 'State name review',
    });
    await user.clear(stateName);
    await user.type(stateName, 'not valid');
    await user.tab();

    expect(stateName).toHaveValue('review');
    expect(
      screen.getByText(/State names must start with a letter/)
    ).toBeInTheDocument();
  });

  it('allows a valid State name inherited by ordinary objects', async () => {
    const user = userEvent.setup();
    render(<BuilderHarness />);

    const stateName = screen.getByRole('textbox', {
      name: 'State name review',
    });
    await user.clear(stateName);
    await user.type(stateName, 'constructor');
    await user.tab();

    expect(
      screen.getByRole('textbox', { name: 'State name constructor' })
    ).toHaveValue('constructor');
  });

  it('shows the filtered state DAG list after blur', async () => {
    const user = userEvent.setup();
    render(<BuilderHarness controllerDAGs={['triage']} />);

    const stateDAGs = screen.getByRole('textbox', {
      name: 'Callable DAGs for default',
    });
    await user.type(stateDAGs, 'triage, missing');
    await user.tab();

    expect(stateDAGs).toHaveValue('triage');
  });

  it('requires configured state contents to be cleared before deletion', async () => {
    const user = userEvent.setup();
    render(
      <BuilderHarness controllerDAGs={['triage']} configuredReview={true} />
    );

    await user.click(
      screen.getByRole('button', { name: 'Delete state review' })
    );

    expect(
      screen.getByText(
        'Clear DAGs and transitions from review before deleting the state.'
      )
    ).toBeInTheDocument();
    expect(
      screen.getByRole('textbox', { name: 'State name review' })
    ).toBeInTheDocument();
  });

  it('shows a retry action when compatible DAGs cannot be loaded', async () => {
    const user = userEvent.setup();
    const retry = vi.fn();
    render(
      <BuilderHarness
        dagOptionsError="Could not load compatible DAGs"
        onRetryDAGOptions={retry}
      />
    );

    expect(screen.getByText('Could not load compatible DAGs')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(retry).toHaveBeenCalledOnce();
  });
});
