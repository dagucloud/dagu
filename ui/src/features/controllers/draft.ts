// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { parseDocument, stringify } from 'yaml';

import type {
  ControllerDefinition,
  ControllerState,
  ControllerValidationIssue,
} from './types';

export const ROUTER_INSTRUCTION = '${{.RouterInstruction}}';
export const DEFAULT_MAX_TURNS = 100;

const STATE_NAME = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/;
const CONTROLLER_ID = /^ctrl_[a-z2-7]{16}$/;

type ParseResult = {
  definition: ControllerDefinition | null;
  issues: ControllerValidationIssue[];
  builderRepresentable: boolean;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function issue(
  path: string,
  message: string,
  code = 'invalid_controller'
): ControllerValidationIssue {
  return { code, path, message };
}

function unknownFields(
  value: Record<string, unknown>,
  allowed: readonly string[],
  path: string
): ControllerValidationIssue[] {
  return Object.keys(value)
    .filter((key) => !allowed.includes(key))
    .map((key) =>
      issue(`${path}.${key}`, `Unknown field ${key}`, 'unknown_field')
    );
}

function stringArray(value: unknown): string[] | null {
  if (!Array.isArray(value) || value.some((item) => typeof item !== 'string')) {
    return null;
  }
  return [...value];
}

function hasOnlyFields(
  value: Record<string, unknown>,
  allowed: readonly string[]
): boolean {
  return Object.keys(value).every((key) => allowed.includes(key));
}

function isBuilderRepresentable(value: Record<string, unknown>): boolean {
  if (
    !hasOnlyFields(value, [
      'type',
      'version',
      'id',
      'name',
      'description',
      'maxTurns',
      'labels',
      'llm',
      'dags',
      'states',
    ]) ||
    value.type !== 'controller' ||
    value.version !== 1 ||
    (value.id !== undefined && typeof value.id !== 'string') ||
    typeof value.name !== 'string' ||
    typeof value.description !== 'string' ||
    (value.maxTurns !== undefined &&
      (typeof value.maxTurns !== 'number' ||
        !Number.isInteger(value.maxTurns) ||
        !Number.isFinite(value.maxTurns))) ||
    (value.labels !== undefined && stringArray(value.labels) === null) ||
    stringArray(value.dags) === null ||
    !isRecord(value.llm) ||
    !isRecord(value.states)
  ) {
    return false;
  }

  const llm = value.llm;
  if (
    !hasOnlyFields(llm, ['provider', 'model', 'system']) ||
    typeof llm.provider !== 'string' ||
    typeof llm.model !== 'string' ||
    (llm.system !== undefined && typeof llm.system !== 'string')
  ) {
    return false;
  }

  return Object.values(value.states).every((state) => {
    if (
      !isRecord(state) ||
      !hasOnlyFields(state, [
        'description',
        'dags',
        'transitions',
        'terminal',
      ]) ||
      typeof state.description !== 'string' ||
      (state.dags !== undefined && stringArray(state.dags) === null) ||
      (state.terminal !== undefined &&
        state.terminal !== 'succeeded' &&
        state.terminal !== 'failed')
    ) {
      return false;
    }
    if (state.transitions === undefined) return true;
    if (!Array.isArray(state.transitions)) return false;
    return state.transitions.every(
      (transition) =>
        isRecord(transition) &&
        hasOnlyFields(transition, ['to', 'when']) &&
        typeof transition.to === 'string' &&
        typeof transition.when === 'string'
    );
  });
}

export function createControllerDraft(workspace = ''): ControllerDefinition {
  return {
    type: 'controller',
    version: 1,
    name: '',
    description: '',
    maxTurns: DEFAULT_MAX_TURNS,
    labels: workspace ? [`workspace=${workspace}`] : [],
    llm: {
      provider: 'openai',
      model: '',
      system: ROUTER_INSTRUCTION,
    },
    dags: [],
    states: {
      default: {
        description: '',
        dags: [],
        transitions: [],
      },
    },
  };
}

export function validateControllerDefinition(
  definition: ControllerDefinition,
  options: { requireId?: boolean } = {}
): ControllerValidationIssue[] {
  const issues: ControllerValidationIssue[] = [];
  if (definition.type !== 'controller') {
    issues.push(issue('type', 'Type must be controller'));
  }
  if (definition.version !== 1) {
    issues.push(issue('version', 'Version must be 1'));
  }
  if (options.requireId && !definition.id) {
    issues.push(issue('id', 'ID is required'));
  }
  if (definition.id && !CONTROLLER_ID.test(definition.id)) {
    issues.push(issue('id', 'ID is not a valid Controller ID'));
  }
  if (!definition.name) {
    issues.push(issue('name', 'Name is required'));
  }
  if (!definition.description?.trim()) {
    issues.push(issue('description', 'Description is required'));
  }
  if (/^[\p{White_Space}]|[\p{White_Space}]$/u.test(definition.name)) {
    issues.push(issue('name', 'Name cannot start or end with whitespace'));
  }
  if (definition.name.includes('\n') || definition.name.includes('\r')) {
    issues.push(issue('name', 'Name must be one line'));
  }
  if (definition.maxTurns < 2 || definition.maxTurns > 1000) {
    issues.push(issue('maxTurns', 'Max turns must be between 2 and 1000'));
  }
  if (!definition.llm.provider) {
    issues.push(issue('llm.provider', 'Provider is required'));
  }
  if (!definition.llm.model) {
    issues.push(issue('llm.model', 'Model is required'));
  }
  if (definition.llm.system !== undefined) {
    const systemIssue = validateSystemPrompt(definition.llm.system);
    if (systemIssue) issues.push(issue('llm.system', systemIssue));
  }
  if (!definition.states.default) {
    issues.push(issue('states.default', 'The default state is required'));
  }
  if (Object.keys(definition.states).length > 64) {
    issues.push(issue('states', 'At most 64 states are allowed'));
  }
  const allowedDAGs = new Set(definition.dags);
  let transitionCount = 0;
  for (const [name, state] of Object.entries(definition.states)) {
    if (!STATE_NAME.test(name)) {
      issues.push(issue(`states.${name}`, 'State name is invalid'));
    }
    if (!state.description?.trim()) {
      issues.push(
        issue(`states.${name}.description`, 'State description is required')
      );
    }
    for (const dag of state.dags) {
      if (!allowedDAGs.has(dag)) {
        issues.push(
          issue(
            `states.${name}.dags`,
            `${dag} is not in the Controller DAG allowlist`
          )
        );
      }
    }
    transitionCount += state.transitions.length;
    if (
      state.terminal &&
      (state.dags.length > 0 || state.transitions.length > 0)
    ) {
      issues.push(
        issue(
          `states.${name}.terminal`,
          'A terminal state cannot contain DAGs or transitions'
        )
      );
    }
    if (
      !state.terminal &&
      state.dags.length === 0 &&
      state.transitions.length === 0
    ) {
      issues.push(
        issue(
          `states.${name}`,
          'A non-terminal state needs a DAG or an outgoing transition'
        )
      );
    }
    for (const transition of state.transitions) {
      if (!definition.states[transition.to]) {
        issues.push(
          issue(
            `states.${name}.transitions`,
            `Transition destination ${transition.to} does not exist`
          )
        );
      }
      if (!transition.when) {
        issues.push(
          issue(
            `states.${name}.transitions`,
            'Transition condition is required'
          )
        );
      }
    }
  }
  if (transitionCount > 256) {
    issues.push(issue('states', 'At most 256 transitions are allowed'));
  }
  return issues;
}

export function validateSystemPrompt(value: string): string | null {
  if (new TextEncoder().encode(value).length > 16_384) {
    return 'System prompt must be 16 KiB or less';
  }
  if (!value.startsWith(ROUTER_INSTRUCTION)) {
    return `System prompt must start with ${ROUTER_INSTRUCTION}`;
  }
  const suffix = value.slice(ROUTER_INSTRUCTION.length);
  if (suffix && !suffix.startsWith('\n\n')) {
    return 'Custom system instructions must follow one blank line';
  }
  const custom = suffix ? suffix.slice(2) : '';
  if (custom && custom.trim().length === 0) {
    return 'Custom system instructions cannot contain only whitespace';
  }
  if (custom.includes('${{')) {
    return 'Custom system instructions cannot contain another template expression';
  }
  return null;
}

export function systemSuffix(system?: string): string | null {
  const effective = system ?? ROUTER_INSTRUCTION;
  if (validateSystemPrompt(effective)) return null;
  return effective === ROUTER_INSTRUCTION
    ? ''
    : effective.slice(ROUTER_INSTRUCTION.length + 2);
}

export function withSystemSuffix(suffix: string): string {
  return suffix ? `${ROUTER_INSTRUCTION}\n\n${suffix}` : ROUTER_INSTRUCTION;
}

function parseState(
  value: unknown,
  path: string,
  issues: ControllerValidationIssue[]
): ControllerState | null {
  if (!isRecord(value)) {
    issues.push(issue(path, 'State must be an object'));
    return null;
  }
  issues.push(
    ...unknownFields(
      value,
      ['description', 'dags', 'transitions', 'terminal'],
      path
    )
  );
  const dags = stringArray(value.dags ?? []);
  if (!dags) issues.push(issue(`${path}.dags`, 'DAGs must be a list of names'));
  const transitionsValue = value.transitions ?? [];
  const transitions: { to: string; when: string }[] = [];
  if (!Array.isArray(transitionsValue)) {
    issues.push(issue(`${path}.transitions`, 'Transitions must be a list'));
  } else {
    transitionsValue.forEach((transition, index) => {
      const transitionPath = `${path}.transitions.${index}`;
      if (!isRecord(transition)) {
        issues.push(issue(transitionPath, 'Transition must be an object'));
        return;
      }
      issues.push(...unknownFields(transition, ['to', 'when'], transitionPath));
      if (
        typeof transition.to !== 'string' ||
        typeof transition.when !== 'string'
      ) {
        issues.push(
          issue(transitionPath, 'Transition needs string to and when fields')
        );
        return;
      }
      transitions.push({ to: transition.to, when: transition.when });
    });
  }
  if (
    value.terminal !== undefined &&
    value.terminal !== 'succeeded' &&
    value.terminal !== 'failed'
  ) {
    issues.push(
      issue(`${path}.terminal`, 'Terminal must be succeeded or failed')
    );
  }
  return {
    description: typeof value.description === 'string' ? value.description : '',
    dags: dags ?? [],
    transitions,
    terminal:
      value.terminal === 'succeeded' || value.terminal === 'failed'
        ? value.terminal
        : undefined,
  };
}

export function parseControllerYAML(source: string): ParseResult {
  const document = parseDocument(source, {
    prettyErrors: true,
    uniqueKeys: true,
  });
  if (document.errors.length > 0) {
    return {
      definition: null,
      issues: document.errors.map((error) =>
        issue('$', error.message, 'invalid_yaml')
      ),
      builderRepresentable: false,
    };
  }
  const value = document.toJS() as unknown;
  if (!isRecord(value)) {
    return {
      definition: null,
      issues: [issue('$', 'Controller YAML must be an object')],
      builderRepresentable: false,
    };
  }
  const issues = unknownFields(
    value,
    [
      'type',
      'version',
      'id',
      'name',
      'description',
      'maxTurns',
      'labels',
      'llm',
      'dags',
      'states',
    ],
    '$'
  );
  const llm = isRecord(value.llm) ? value.llm : {};
  if (!isRecord(value.llm))
    issues.push(issue('llm', 'LLM configuration is required'));
  issues.push(...unknownFields(llm, ['provider', 'model', 'system'], 'llm'));
  const states: Record<string, ControllerState> = {};
  if (!isRecord(value.states)) {
    issues.push(issue('states', 'States must be an object'));
  } else {
    for (const [name, state] of Object.entries(value.states)) {
      const parsed = parseState(state, `states.${name}`, issues);
      if (parsed) states[name] = parsed;
    }
  }
  const labels = stringArray(value.labels ?? []);
  const dags = stringArray(value.dags ?? []);
  if (!labels) issues.push(issue('labels', 'Labels must be a list of strings'));
  if (!dags) issues.push(issue('dags', 'DAGs must be a list of names'));
  const definition: ControllerDefinition = {
    type:
      value.type === 'controller' ? 'controller' : ('invalid' as 'controller'),
    version: value.version === 1 ? 1 : (value.version as 1),
    id: typeof value.id === 'string' ? value.id : undefined,
    name: typeof value.name === 'string' ? value.name : '',
    description: typeof value.description === 'string' ? value.description : '',
    maxTurns:
      typeof value.maxTurns === 'number' ? value.maxTurns : DEFAULT_MAX_TURNS,
    labels: labels ?? [],
    llm: {
      provider: typeof llm.provider === 'string' ? llm.provider : '',
      model: typeof llm.model === 'string' ? llm.model : '',
      system: typeof llm.system === 'string' ? llm.system : undefined,
    },
    dags: dags ?? [],
    states,
  };
  issues.push(...validateControllerDefinition(definition));
  return {
    definition,
    issues,
    builderRepresentable: isBuilderRepresentable(value),
  };
}

export function serializeControllerDefinition(
  definition: ControllerDefinition
): string {
  const value: Record<string, unknown> = {
    type: 'controller',
    version: 1,
  };
  if (definition.id) value.id = definition.id;
  value.name = definition.name;
  value.description = definition.description ?? '';
  value.maxTurns = definition.maxTurns;
  value.labels = definition.labels;
  value.llm = definition.llm;
  value.dags = definition.dags;
  value.states = definition.states;
  return stringify(value, { lineWidth: 0 });
}
