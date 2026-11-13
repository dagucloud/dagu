// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { StepInspectorProps } from '../../contracts';
import { makeModel, makeStep } from '../../canvas/__tests__/fixtures';
import { catalogSample } from '../../testing/fixtures';
import { sectionOfPath, StepInspector } from '../StepInspector';
import { withoutToolKeys } from '../sections/Parameters';
import { schemaSample } from './fixtures';

// The sections have their own tests: here they only show that they are there.
function stub(name: string) {
  return (props: { fieldIssues?: Record<string, unknown> }) => (
    <div
      data-testid={`section-${name}`}
      data-fields={Object.keys(props.fieldIssues ?? {}).join(',')}
    />
  );
}
vi.mock('../sections/General', () => ({ GeneralSection: stub('General') }));
vi.mock('../sections/Flow', () => ({ FlowSection: stub('Flow') }));
vi.mock('../sections/Parameters', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../sections/Parameters')>()),
  ParametersSection: stub('Parameters'),
}));
vi.mock('../sections/StepYaml', () => ({ StepYamlSection: stub('StepYaml') }));
vi.mock('../sections/Action', () => ({
  ActionSection: stub('Action'),
  isRunStep: (step: { kind: string }) => step.kind === 'run',
}));
vi.mock('../sections/tools/ToolsSection', () => ({
  ToolsSection: stub('Tools'),
}));

function setup(action: string, issues: StepInspectorProps['issues'] = []) {
  const model = makeModel([
    makeStep({
      key: 'steps[0]',
      kind: 'action',
      action,
      value: { action, with: {} },
    }),
  ]);
  render(
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

describe('Tools section of the step inspector', () => {
  it('is there for a chat step only', () => {
    setup('chat.completion');
    expect(screen.getByTestId('section-Tools')).toBeInTheDocument();
  });

  it('is absent for another action', () => {
    setup('http.request');
    expect(screen.queryByTestId('section-Tools')).toBeNull();
  });

  it('counts the issues of the tools in its own header', () => {
    setup('chat.completion', [
      {
        severity: ValidationIssueSeverity.error,
        source: ValidationIssueSource.build,
        message: 'unknown DAG',
        stepPath: 'steps[0]',
        fieldPath: 'steps[0].with.dags[0]',
      },
    ]);
    const header = screen.getByRole('button', { name: /Tools/ });
    expect(header).toHaveTextContent('1');
    // The same issue is not counted again in another section of the form.
    for (const name of ['Model', 'Prompt', 'Options']) {
      expect(screen.getByRole('button', { name })).not.toHaveTextContent('1');
    }
  });
});

describe('sectionOfPath', () => {
  it.each([
    ['with.tools', 'tools'],
    ['with.dags', 'tools'],
    ['with.dags[1].params.x', 'tools'],
    ['with.tools.mcp[1].url', 'tools'],
    ['with.max_tools', 'tools'],
    ['with.tool_result_max_bytes', 'tools'],
    ['with.prompt', 'parameters'],
    ['with.tools_other', 'parameters'],
  ])('%s opens %s', (path, section) => {
    expect(sectionOfPath(path)).toBe(section);
  });

  // Each key that a section of the chat form or of the agent form edits opens
  // that section; a key that no section edits opens none.
  it.each([
    ['with.endpoint', 'chat', 'model'],
    ['with.endpoint.name', 'chat', 'model'],
    ['with.thinking.effort', 'chat', 'options'],
    ['with.messages[0].role', 'chat', 'prompt'],
    ['with', 'chat', 'prompt'],
    ['with.unknown', 'chat', null],
    ['with.endpoint.model', 'agentRun', 'model'],
    ['with.dags[0]', 'agentRun', 'tools'],
    ['with.tools.builtin.read', 'agentRun', 'tools'],
    ['with.max_tool_calls', 'agentRun', 'limits'],
    ['with.model', 'agentRun', null],
    ['with.effort', 'agentRun', null],
  ] as const)('%s of a %s form opens %s', (path, form, section) => {
    expect(sectionOfPath(path, form)).toBe(section);
  });
});

describe('withoutToolKeys', () => {
  it('drops the properties that the Tools section owns', () => {
    const schema = withoutToolKeys({
      type: 'object',
      required: ['prompt', 'tools'],
      properties: {
        prompt: { type: 'string' },
        dags: { type: 'array' },
        tools: { type: 'object' },
        max_tools: { type: 'integer' },
        tool_result_max_bytes: { type: 'integer' },
      },
    });
    expect(Object.keys(schema.properties ?? {})).toEqual(['prompt']);
    expect(schema.required).toEqual(['prompt']);
  });
});
