// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type {
  ReferenceCandidate,
  StepView,
  ValidationIssue,
} from '../../contracts';
import { makeStep } from '../../canvas/__tests__/fixtures';
import {
  declaredSecrets,
  readSecretList,
  SecuritySection,
} from '../sections/Security';

afterEach(() => vi.clearAllMocks());

const secret = (name: string): ReferenceCandidate => ({
  token: `\${${name}}`,
  label: name,
  group: 'secrets',
  known: 'static',
});
const GIT = secret('GIT_TOKEN');
const NPM = secret('NPM_TOKEN');
const PARAM: ReferenceCandidate = {
  token: '${REGION}',
  label: 'REGION',
  group: 'params',
  known: 'static',
};

const run = (extra: Record<string, unknown> = {}): StepView =>
  makeStep({
    key: 'steps[0]',
    kind: 'run',
    runPreview: 'make',
    value: { run: 'make', ...extra },
  });

const action = (
  name: string,
  withValue: Record<string, unknown>,
  extra: Record<string, unknown> = {}
): StepView =>
  makeStep({
    key: 'steps[0]',
    kind: 'action',
    action: name,
    value: { action: name, with: withValue, ...extra },
  });

function setup(
  step: StepView,
  options: {
    candidates?: ReferenceCandidate[];
    readOnly?: boolean;
    fieldIssues?: Record<string, ValidationIssue[]>;
    agentDag?: boolean;
  } = {}
) {
  const onEdit = vi.fn();
  const view = render(
    <SecuritySection
      step={step}
      readOnly={options.readOnly ?? false}
      onEdit={onEdit}
      fieldIssues={options.fieldIssues ?? {}}
      candidates={options.candidates ?? [GIT, NPM, PARAM]}
      count={0}
      agentDag={options.agentDag}
    />
  );
  return { ...view, onEdit };
}

const op = (path: string[], value: unknown) => ({
  kind: 'setStepField',
  step: 'steps[0]',
  path,
  value,
});

describe('declaredSecrets and readSecretList', () => {
  it('keeps the names of the secrets of the DAG and no other reference', () => {
    expect(declaredSecrets([GIT, PARAM, NPM])).toEqual([
      'GIT_TOKEN',
      'NPM_TOKEN',
    ]);
  });

  it('reads a list of names, and nothing for another shape', () => {
    expect(readSecretList(undefined)).toEqual([]);
    expect(readSecretList(['A', 'B'])).toEqual(['A', 'B']);
    expect(readSecretList('A')).toBeNull();
    expect(readSecretList(['A', 3])).toBeNull();
  });
});

describe('Security section: secrets', () => {
  it('adds a declared secret to the list of the step', () => {
    const { onEdit } = setup(run());
    fireEvent.change(screen.getByLabelText('Add a secret'), {
      target: { value: 'GIT_TOKEN' },
    });
    expect(onEdit).toHaveBeenCalledWith(op(['secrets'], ['GIT_TOKEN']));
  });

  it('offers only the declared secrets that the list does not have', () => {
    setup(run({ secrets: ['GIT_TOKEN'] }));
    const select = screen.getByLabelText('Add a secret');
    expect(
      within(select)
        .getAllByRole('option')
        .map((option) => option.textContent)
    ).toEqual(['Add a secret', 'NPM_TOKEN']);
  });

  it('removes a name, and removes the key with the last name', () => {
    const { onEdit } = setup(run({ secrets: ['GIT_TOKEN', 'NPM_TOKEN'] }));
    fireEvent.click(
      screen.getByRole('button', { name: 'Remove secret GIT_TOKEN' })
    );
    expect(onEdit).toHaveBeenLastCalledWith(op(['secrets'], ['NPM_TOKEN']));
    const last = setup(run({ secrets: ['NPM_TOKEN'] }));
    fireEvent.click(
      within(last.container).getByRole('button', {
        name: 'Remove secret NPM_TOKEN',
      })
    );
    expect(last.onEdit).toHaveBeenCalledWith(op(['secrets'], undefined));
  });

  it('is not drawn when the DAG has no secret and the step has no list', () => {
    const { container } = setup(action('http.request', { url: 'x' }), {
      candidates: [PARAM],
    });
    expect(container).toBeEmptyDOMElement();
  });

  it('keeps the list of a step with names that no secret declares, with its issue', () => {
    const issue: ValidationIssue = {
      severity: ValidationIssueSeverity.error,
      source: ValidationIssueSource.build,
      message: 'secret "OLD" is not declared',
    };
    setup(action('http.request', { url: 'x' }, { secrets: ['OLD'] }), {
      candidates: [PARAM],
      fieldIssues: { 'secrets[0]': [issue] },
    });
    expect(screen.getByText('OLD')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('not declared');
    // Nothing to add: the DAG declares no secret.
    expect(screen.queryByLabelText('Add a secret')).toBeNull();
  });

  it('leaves a value that is not a list of names to the YAML', () => {
    setup(run({ secrets: 'GIT_TOKEN' }));
    expect(screen.getByTestId('ve-yaml-only')).toHaveTextContent('secrets');
    expect(screen.queryByLabelText('Add a secret')).toBeNull();
  });

  it('gives a reader the list and no control', () => {
    setup(run({ secrets: ['GIT_TOKEN'] }), { readOnly: true });
    expect(screen.getByText('GIT_TOKEN')).toBeInTheDocument();
    expect(screen.queryByLabelText('Add a secret')).toBeNull();
    expect(screen.queryByRole('button', { name: /Remove secret/ })).toBeNull();
    expect(screen.getByLabelText('Untrusted')).toBeDisabled();
  });
});

describe('Security section: untrusted', () => {
  it('switches the key on and off on a shell step', () => {
    const { onEdit } = setup(run());
    fireEvent.click(screen.getByLabelText('Untrusted'));
    expect(onEdit).toHaveBeenCalledWith(op(['untrusted'], true));
    const marked = setup(run({ untrusted: true }));
    fireEvent.click(within(marked.container).getByLabelText('Untrusted'));
    expect(marked.onEdit).toHaveBeenCalledWith(op(['untrusted'], undefined));
  });

  it('offers the switch on a harness step and on no other action', () => {
    setup(action('harness.run', { provider: 'claude' }));
    expect(screen.getByLabelText('Untrusted')).toBeInTheDocument();
  });

  it('has no switch on a step that cannot take the key', () => {
    const http = setup(action('http.request', { url: 'x' }));
    expect(http.container.querySelector('[data-field-path="untrusted"]')).toBe(
      null
    );
    const container = setup(run({ container: { image: 'alpine' } }));
    expect(
      container.container.querySelector('[data-field-path="untrusted"]')
    ).toBe(null);
  });

  it('shows no list of secrets on an untrusted step, because it gets none', () => {
    setup(run({ untrusted: true }));
    expect(screen.queryByLabelText('Add a secret')).toBeNull();
    expect(screen.queryByText('Secrets')).toBeNull();
  });

  it('shows the explanation, no badge and no switch on an agent step', () => {
    setup(action('agent.run', { provider: 'claude', prompt: 'x' }));
    expect(screen.queryByTestId('untrusted-badge')).toBeNull();
    expect(
      screen.getByText('A step with a CLI provider is always untrusted.')
    ).toBeInTheDocument();
    expect(screen.queryByRole('switch')).toBeNull();
    expect(screen.queryByLabelText('Add a secret')).toBeNull();
  });

  it('shows the mark on an agent step that says untrusted: true', () => {
    setup(action('agent.run', {}, { untrusted: true }));
    expect(screen.getByTestId('untrusted-badge')).toBeInTheDocument();
    expect(screen.queryByRole('switch')).toBeNull();
  });

  it('shows no mark on an agent step that names no provider yet', () => {
    setup(action('agent.run', {}));
    expect(screen.queryByTestId('untrusted-badge')).toBeNull();
    expect(screen.queryByRole('switch')).toBeNull();
  });

  it('leaves a redundant untrusted key of an agent step to the YAML', () => {
    setup(action('agent.run', { provider: 'claude' }, { untrusted: false }));
    expect(screen.getByTestId('ve-yaml-only')).toHaveTextContent('untrusted');
    expect(screen.queryByRole('switch')).toBeNull();
  });

  it('shows the effective value in a DAG that has an agent step: untrusted by default', () => {
    const { onEdit } = setup(run(), { agentDag: true });
    const toggle = screen.getByLabelText('Untrusted');
    expect(toggle).toBeChecked();
    expect(
      screen.getByText('Untrusted by default: the DAG has an agent step.')
    ).toBeInTheDocument();
    // Off writes untrusted: false, the way to a trusted step.
    fireEvent.click(toggle);
    expect(onEdit).toHaveBeenCalledWith(op(['untrusted'], false));
  });

  it('shows a step with untrusted: false as trusted, and switches it back by removing the key', () => {
    const { onEdit } = setup(run({ untrusted: false }), { agentDag: true });
    const toggle = screen.getByLabelText('Untrusted');
    expect(toggle).not.toBeChecked();
    expect(
      screen.getByText('Trusted: the key untrusted is false.')
    ).toBeInTheDocument();
    fireEvent.click(toggle);
    expect(onEdit).toHaveBeenCalledWith(op(['untrusted'], undefined));
  });

  // Secrets alone do not make the step trusted: the DAG refuses it.
  it('shows a step that lists secrets as untrusted, and untrusted: false clears it', () => {
    const { onEdit } = setup(run({ secrets: ['GIT_TOKEN'] }), {
      agentDag: true,
    });
    const toggle = screen.getByLabelText('Untrusted');
    expect(toggle).toBeChecked();
    expect(toggle).toBeEnabled();
    expect(
      screen.getByText('Untrusted by default: the DAG has an agent step.')
    ).toBeInTheDocument();
    expect(screen.getByText('GIT_TOKEN')).toBeInTheDocument();
    fireEvent.click(toggle);
    expect(onEdit).toHaveBeenCalledWith(op(['untrusted'], false));
  });

  it('shows the error of secrets without untrusted: false under the list', () => {
    setup(run({ secrets: ['GIT_TOKEN'] }), {
      agentDag: true,
      fieldIssues: {
        secrets: [
          {
            severity: ValidationIssueSeverity.error,
            source: ValidationIssueSource.build,
            message: 'step lists secrets but is untrusted by default',
          },
        ],
      },
    });
    expect(screen.getByRole('alert')).toHaveTextContent(
      'step lists secrets but is untrusted by default'
    );
  });

  it('shows no list of secrets to add on a step that is untrusted by default', () => {
    setup(run(), { agentDag: true });
    expect(screen.queryByLabelText('Add a secret')).toBeNull();
  });

  it('keeps the old behavior in a DAG without an agent step', () => {
    const { onEdit } = setup(run(), { agentDag: false });
    const toggle = screen.getByLabelText('Untrusted');
    expect(toggle).not.toBeChecked();
    fireEvent.click(toggle);
    expect(onEdit).toHaveBeenCalledWith(op(['untrusted'], true));
  });

  it('has no switch on a step with another action in a DAG that has an agent step', () => {
    const http = setup(action('http.request', { url: 'x' }), {
      agentDag: true,
    });
    expect(http.container.querySelector('[data-field-path="untrusted"]')).toBe(
      null
    );
  });

  it('shows the issues of the key', () => {
    const issue: ValidationIssue = {
      severity: ValidationIssueSeverity.error,
      source: ValidationIssueSource.build,
      message: 'untrusted: false is not valid here',
    };
    setup(action('agent.run', { provider: 'claude' }, { untrusted: false }), {
      fieldIssues: { untrusted: [issue] },
    });
    expect(screen.getByRole('alert')).toHaveTextContent('not valid here');
  });
});
