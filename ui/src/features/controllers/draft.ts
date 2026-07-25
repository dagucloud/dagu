// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { parseDocument, stringify } from 'yaml';

import type {
  ControllerDefinition,
  ControllerState,
  ControllerValidationIssue,
} from './types';
import {
  CONTROLLER_DAG_NAME_PATTERN,
  CONTROLLER_ID_PATTERN,
  CONTROLLER_STATE_NAME_PATTERN,
  DEFAULT_CONTROLLER_MAX_TURNS,
  DEFAULT_CONTROLLER_LLM_PROVIDER,
  MAX_CONTROLLER_DAGS,
  MAX_CONTROLLER_DESCRIPTION_BYTES,
  MAX_CONTROLLER_MAX_TURNS,
  MAX_CONTROLLER_NAME_BYTES,
  MAX_CONTROLLER_NAME_CODE_POINTS,
  MAX_CONTROLLER_STATES,
  MAX_CONTROLLER_SYSTEM_PROMPT_BYTES,
  MAX_CONTROLLER_TRANSITIONS,
  MIN_CONTROLLER_MAX_TURNS,
  hasNonWhitespace,
  utf8ByteLength,
  validateControllerLabels,
} from './constraints';

export const ROUTER_INSTRUCTION = '${{.RouterInstruction}}';

const CONTROLLER_FIELDS = [
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
] as const;
const LLM_FIELDS = ['provider', 'model', 'system'] as const;
const STATE_FIELDS = [
  'description',
  'dags',
  'transitions',
  'terminal',
] as const;
const TRANSITION_FIELDS = ['to', 'when'] as const;

type ControllerDefinitionOperation = 'create' | 'update';

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
    !hasOnlyFields(value, CONTROLLER_FIELDS) ||
    value.type !== 'controller' ||
    value.version !== 1 ||
    (value.id !== undefined && typeof value.id !== 'string') ||
    typeof value.name !== 'string' ||
    (value.description !== undefined &&
      typeof value.description !== 'string') ||
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
    !hasOnlyFields(llm, LLM_FIELDS) ||
    typeof llm.provider !== 'string' ||
    typeof llm.model !== 'string' ||
    (llm.system !== undefined && typeof llm.system !== 'string')
  ) {
    return false;
  }

  return Object.values(value.states).every((state) => {
    if (
      !isRecord(state) ||
      !hasOnlyFields(state, STATE_FIELDS) ||
      (state.description !== undefined &&
        typeof state.description !== 'string') ||
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
        hasOnlyFields(transition, TRANSITION_FIELDS) &&
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
    maxTurns: DEFAULT_CONTROLLER_MAX_TURNS,
    labels: workspace ? [`workspace=${workspace}`] : [],
    llm: {
      provider: DEFAULT_CONTROLLER_LLM_PROVIDER,
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
  operation: ControllerDefinitionOperation = 'create'
): ControllerValidationIssue[] {
  const issues: ControllerValidationIssue[] = [];
  if (definition.type !== 'controller') {
    issues.push(issue('type', 'Type must be controller'));
  }
  if (definition.version !== 1) {
    issues.push(issue('version', 'Version must be 1'));
  }
  if (operation === 'create' && definition.id !== undefined) {
    issues.push(issue('id', 'ID is generated when the Controller is created'));
  }
  if (operation === 'update' && !definition.id) {
    issues.push(issue('id', 'ID is required'));
  }
  if (definition.id && !CONTROLLER_ID_PATTERN.test(definition.id)) {
    issues.push(issue('id', 'ID is not a valid Controller ID'));
  }
  if (!definition.name) {
    issues.push(issue('name', 'Name is required'));
  }
  if (
    Array.from(definition.name).length > MAX_CONTROLLER_NAME_CODE_POINTS ||
    utf8ByteLength(definition.name) > MAX_CONTROLLER_NAME_BYTES
  ) {
    issues.push(
      issue(
        'name',
        `Name must be at most ${MAX_CONTROLLER_NAME_CODE_POINTS} characters and ${MAX_CONTROLLER_NAME_BYTES} bytes`
      )
    );
  }
  if (
    utf8ByteLength(definition.description ?? '') >
    MAX_CONTROLLER_DESCRIPTION_BYTES
  ) {
    issues.push(
      issue(
        'description',
        `Description must be ${MAX_CONTROLLER_DESCRIPTION_BYTES} bytes or less`
      )
    );
  }
  if (/^[\p{White_Space}]|[\p{White_Space}]$/u.test(definition.name)) {
    issues.push(issue('name', 'Name cannot start or end with whitespace'));
  }
  if (/[\n\v\f\r\u0085\u2028\u2029]/u.test(definition.name)) {
    issues.push(issue('name', 'Name must be one line'));
  }
  if (/\p{Cc}/u.test(definition.name)) {
    issues.push(issue('name', 'Name cannot contain control characters'));
  }
  if (
    !Number.isInteger(definition.maxTurns) ||
    definition.maxTurns < MIN_CONTROLLER_MAX_TURNS ||
    definition.maxTurns > MAX_CONTROLLER_MAX_TURNS
  ) {
    issues.push(
      issue(
        'maxTurns',
        `Max turns must be an integer between ${MIN_CONTROLLER_MAX_TURNS} and ${MAX_CONTROLLER_MAX_TURNS}`
      )
    );
  }
  const labelsIssue = validateControllerLabels(definition.labels);
  if (labelsIssue) issues.push(issue('labels', labelsIssue));
  if (!definition.llm.provider) {
    issues.push(issue('llm.provider', 'Provider is required'));
  }
  if (!hasNonWhitespace(definition.llm.model)) {
    issues.push(issue('llm.model', 'Model must contain non-whitespace text'));
  }
  if (definition.llm.system !== undefined) {
    const systemIssue = validateSystemPrompt(definition.llm.system);
    if (systemIssue) issues.push(issue('llm.system', systemIssue));
  }
  if (!Object.prototype.hasOwnProperty.call(definition.states, 'default')) {
    issues.push(issue('states.default', 'The default state is required'));
  }
  if (Object.keys(definition.states).length > MAX_CONTROLLER_STATES) {
    issues.push(
      issue('states', `At most ${MAX_CONTROLLER_STATES} states are allowed`)
    );
  }

  if (definition.dags.length > MAX_CONTROLLER_DAGS) {
    issues.push(
      issue('dags', `At most ${MAX_CONTROLLER_DAGS} DAGs are allowed`)
    );
  }
  const allowedDAGs = new Set<string>();
  definition.dags.forEach((dag, index) => {
    const path = `dags[${index}]`;
    if (!CONTROLLER_DAG_NAME_PATTERN.test(dag)) {
      issues.push(issue(path, 'DAG name is invalid'));
    }
    if (allowedDAGs.has(dag)) {
      issues.push(issue(path, `DAG ${dag} is listed more than once`));
    }
    allowedDAGs.add(dag);
  });
  let transitionCount = 0;
  for (const [name, state] of Object.entries(definition.states)) {
    const statePath = `states.${name}`;
    if (!CONTROLLER_STATE_NAME_PATTERN.test(name)) {
      issues.push(issue(statePath, 'State name is invalid'));
    }
    if (
      utf8ByteLength(state.description ?? '') > MAX_CONTROLLER_DESCRIPTION_BYTES
    ) {
      issues.push(
        issue(
          `${statePath}.description`,
          `State description must be ${MAX_CONTROLLER_DESCRIPTION_BYTES} bytes or less`
        )
      );
    }
    const stateDAGs = new Set<string>();
    state.dags.forEach((dag, index) => {
      const path = `${statePath}.dags[${index}]`;
      if (!allowedDAGs.has(dag)) {
        issues.push(
          issue(path, `${dag} is not in the Controller DAG allowlist`)
        );
      }
      if (stateDAGs.has(dag)) {
        issues.push(issue(path, `DAG ${dag} is listed more than once`));
      }
      stateDAGs.add(dag);
    });
    transitionCount += state.transitions.length;
    if (
      state.terminal &&
      (state.dags.length > 0 || state.transitions.length > 0)
    ) {
      issues.push(
        issue(
          `${statePath}.terminal`,
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
          statePath,
          'A non-terminal state needs a DAG or an outgoing transition'
        )
      );
    }
    const transitionDestinations = new Set<string>();
    state.transitions.forEach((transition, index) => {
      const transitionPath = `${statePath}.transitions[${index}]`;
      if (!CONTROLLER_STATE_NAME_PATTERN.test(transition.to)) {
        issues.push(
          issue(`${transitionPath}.to`, 'Transition destination is invalid')
        );
      } else if (
        !Object.prototype.hasOwnProperty.call(definition.states, transition.to)
      ) {
        issues.push(
          issue(
            `${transitionPath}.to`,
            `Transition destination ${transition.to} does not exist`
          )
        );
      }
      if (transitionDestinations.has(transition.to)) {
        issues.push(
          issue(
            `${transitionPath}.to`,
            `Transition destination ${transition.to} is listed more than once`
          )
        );
      }
      transitionDestinations.add(transition.to);
      if (!hasNonWhitespace(transition.when)) {
        issues.push(
          issue(`${transitionPath}.when`, 'Transition condition is required')
        );
      }
      if (utf8ByteLength(transition.when) > MAX_CONTROLLER_DESCRIPTION_BYTES) {
        issues.push(
          issue(
            `${transitionPath}.when`,
            `Transition condition must be ${MAX_CONTROLLER_DESCRIPTION_BYTES} bytes or less`
          )
        );
      }
    });
  }
  if (transitionCount > MAX_CONTROLLER_TRANSITIONS) {
    issues.push(
      issue(
        'states',
        `At most ${MAX_CONTROLLER_TRANSITIONS} transitions are allowed`
      )
    );
  }
  return issues;
}

export function validateSystemPrompt(value: string): string | null {
  if (utf8ByteLength(value) > MAX_CONTROLLER_SYSTEM_PROMPT_BYTES) {
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
  if (suffix && !hasNonWhitespace(custom)) {
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
  issues.push(...unknownFields(value, STATE_FIELDS, path));
  const dags = stringArray(value.dags ?? []);
  if (!dags) issues.push(issue(`${path}.dags`, 'DAGs must be a list of names'));
  const transitionsValue = value.transitions ?? [];
  const transitions: { to: string; when: string }[] = [];
  if (!Array.isArray(transitionsValue)) {
    issues.push(issue(`${path}.transitions`, 'Transitions must be a list'));
  } else {
    transitionsValue.forEach((transition, index) => {
      const transitionPath = `${path}.transitions[${index}]`;
      if (!isRecord(transition)) {
        issues.push(issue(transitionPath, 'Transition must be an object'));
        return;
      }
      issues.push(
        ...unknownFields(transition, TRANSITION_FIELDS, transitionPath)
      );
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

export function parseControllerYAML(
  source: string,
  operation: ControllerDefinitionOperation = 'create'
): ParseResult {
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
  const issues = unknownFields(value, CONTROLLER_FIELDS, '$');
  if (value.id !== undefined && typeof value.id !== 'string') {
    issues.push(issue('id', 'ID must be a string'));
  }
  if (value.maxTurns !== undefined && typeof value.maxTurns !== 'number') {
    issues.push(issue('maxTurns', 'Max turns must be a number'));
  }
  const llm = isRecord(value.llm) ? value.llm : {};
  if (!isRecord(value.llm))
    issues.push(issue('llm', 'LLM configuration is required'));
  issues.push(...unknownFields(llm, LLM_FIELDS, 'llm'));
  if (llm.system !== undefined && typeof llm.system !== 'string') {
    issues.push(issue('llm.system', 'System prompt must be a string'));
  }
  const states = Object.create(null) as Record<string, ControllerState>;
  if (!isRecord(value.states)) {
    issues.push(issue('states', 'States must be an object'));
  } else {
    for (const [name, state] of Object.entries(value.states)) {
      const parsed = parseState(state, `states.${name}`, issues);
      if (parsed) states[name] = parsed;
    }
  }
  const labels = stringArray(value.labels ?? []);
  const dags = stringArray(value.dags);
  if (!labels) issues.push(issue('labels', 'Labels must be a list of strings'));
  if (!dags) {
    issues.push(issue('dags', 'DAGs must be present as a list of names'));
  }
  const definition: ControllerDefinition = {
    type:
      value.type === 'controller' ? 'controller' : ('invalid' as 'controller'),
    version: value.version === 1 ? 1 : (value.version as 1),
    id: typeof value.id === 'string' ? value.id : undefined,
    name: typeof value.name === 'string' ? value.name : '',
    description: typeof value.description === 'string' ? value.description : '',
    maxTurns:
      typeof value.maxTurns === 'number'
        ? value.maxTurns
        : DEFAULT_CONTROLLER_MAX_TURNS,
    labels: labels ?? [],
    llm: {
      provider: typeof llm.provider === 'string' ? llm.provider : '',
      model: typeof llm.model === 'string' ? llm.model : '',
      system: typeof llm.system === 'string' ? llm.system : undefined,
    },
    dags: dags ?? [],
    states,
  };
  issues.push(...validateControllerDefinition(definition, operation));
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
