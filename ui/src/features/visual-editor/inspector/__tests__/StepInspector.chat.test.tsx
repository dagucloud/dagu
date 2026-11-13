// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { StepInspectorProps } from '../../contracts';
import { makeModel, makeStep } from '../../canvas/__tests__/fixtures';
import { catalogSample } from '../../testing/fixtures';
import { sectionOfPath, StepInspector } from '../StepInspector';
import { schemaSample } from './fixtures';

// The Tools section has its own tests: here it only shows its place.
vi.mock('../sections/tools/ToolsSection', () => ({
  ToolsSection: () => <div data-testid="section-Tools" />,
}));
vi.mock('../sections/General', () => ({
  GeneralSection: () => <div data-testid="section-General" />,
}));
vi.mock('../sections/Flow', () => ({
  FlowSection: () => <div data-testid="section-Flow" />,
}));
vi.mock('../sections/Action', () => ({
  ActionSection: () => <div data-testid="section-Action" />,
  isRunStep: (step: { kind: string }) => step.kind === 'run',
}));
vi.mock('../sections/StepYaml', () => ({
  StepYamlSection: () => <div data-testid="section-StepYaml" />,
}));
vi.mock('../sections/chat/OutputSection', () => ({
  OutputSection: () => <div data-testid="section-Output" />,
}));

function setup(
  action: string,
  withValue: Record<string, unknown>,
  issues: StepInspectorProps['issues'] = []
) {
  const model = makeModel([
    makeStep({
      key: 'steps[0]',
      kind: 'action',
      action,
      value: { action, with: withValue },
    }),
  ]);
  return render(
    <StepInspector
      model={model}
      step="steps[0]"
      schema={schemaSample}
      catalog={catalogSample}
      customActions={[]}
      candidates={[]}
      issues={issues}
      readOnly={false}
      onEdit={() => {}}
      onClose={() => {}}
    />
  );
}

function headers(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('section > button')).map(
    (button) => button.textContent ?? ''
  );
}

describe('step inspector, chat.completion', () => {
  it('shows the sections of the chat form in order, Tools after Prompt', () => {
    const { container } = setup('chat.completion', { prompt: 'hi' });
    const form = headers(container).filter((text) =>
      [
        'Model',
        'System message',
        'Prompt',
        'Tools',
        'Output',
        'Options',
      ].includes(text)
    );
    expect(form).toEqual([
      'Model',
      'System message',
      'Prompt',
      'Tools',
      'Output',
      'Options',
    ]);
    expect(headers(container)).not.toContain('Parameters');
  });

  it('starts with Options closed and the other sections open', () => {
    setup('chat.completion', { prompt: 'hi' });
    expect(screen.getByRole('button', { name: 'Options' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
    for (const name of ['Model', 'System message', 'Prompt', 'Output']) {
      expect(screen.getByRole('button', { name })).toHaveAttribute(
        'aria-expanded',
        'true'
      );
    }
  });

  it('keeps every other key of `with` in an Other keys list', () => {
    setup('chat.completion', {
      prompt: 'hi',
      model: 'm',
      stream: false,
      custom_flag: { a: 1 },
    });
    const other = screen.getByTestId('ve-other-keys');
    expect(within(other).getByText('custom_flag')).toBeInTheDocument();
    expect(within(other).queryByText('prompt')).toBeNull();
    expect(within(other).queryByText('stream')).toBeNull();
  });

  it('shows no Other keys list when every key has a place', () => {
    setup('chat.completion', { prompt: 'hi', model: 'm', max_tools: 3 });
    expect(screen.queryByTestId('ve-other-keys')).toBeNull();
  });

  it('counts the issues in the section of their field', () => {
    const issue = (fieldPath: string) => ({
      severity: ValidationIssueSeverity.error,
      source: ValidationIssueSource.build,
      message: 'bad',
      stepPath: 'steps[0]',
      fieldPath: `steps[0].${fieldPath}`,
    });
    setup('chat.completion', { prompt: 'hi' }, [
      issue('with.model'),
      issue('with.system'),
      issue('with.temperature'),
      issue('output_schema'),
    ]);
    for (const name of ['Model', 'System message', 'Options', 'Output']) {
      expect(
        screen.getByRole('button', { name: new RegExp(name) })
      ).toHaveTextContent('1');
    }
    expect(
      screen.getByRole('button', { name: 'Prompt' })
    ).not.toHaveTextContent(/\d/);
  });

  it('leaves the generic form to the other actions', () => {
    setup('http.request', { url: 'https://x.test' });
    expect(
      screen.getByRole('button', { name: /Parameters/ })
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Model' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Options' })).toBeNull();
  });
});

describe('sectionOfPath for a chat step', () => {
  it.each([
    ['with.provider', 'model'],
    ['with.model[1].name', 'model'],
    ['with.base_url', 'model'],
    ['with.system', 'system'],
    ['with.prompt', 'prompt'],
    ['with.messages[0].content', 'prompt'],
    ['with', 'prompt'],
    ['with.tools[0]', 'tools'],
    ['with.temperature', 'options'],
    ['with.max_tool_iterations', 'options'],
    ['output_schema', 'output'],
  ])('%s opens %s', (path, section) => {
    expect(sectionOfPath(path, 'chat')).toBe(section);
  });

  it('gives an unknown key no section', () => {
    expect(sectionOfPath('with.custom', 'chat')).toBeNull();
  });

  it('keeps the generic sections for another action', () => {
    expect(sectionOfPath('with.prompt')).toBe('parameters');
    expect(sectionOfPath('with.model')).toBe('parameters');
    expect(sectionOfPath('output_schema')).toBeNull();
  });
});
