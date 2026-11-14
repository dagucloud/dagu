// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type {
  DagDocModel,
  StepNameMap,
  StepView,
  TextRange,
  ValidationIssue,
} from '../../contracts';
import {
  issuesSample,
  modelArraySteps,
  namesArraySteps,
  yamlHandlers,
  yamlHttpJq,
} from '../../testing/fixtures';
import { parseDagDoc } from '../parse';
import {
  attributeIssues,
  groupIssues,
  locateIssue,
  rangeFromOffsets,
} from '../issues';

function issue(extra: Partial<ValidationIssue>): ValidationIssue {
  return {
    severity: ValidationIssueSeverity.error,
    source: ValidationIssueSource.build,
    message: 'm',
    ...extra,
  };
}

describe('groupIssues', () => {
  it('groups the sample by step key', () => {
    const groups = groupIssues(modelArraySteps, issuesSample, namesArraySteps);
    expect(groups.byStep).toEqual({
      'steps[1]': [issuesSample[0]],
      'steps[2]': [issuesSample[2]],
    });
    expect(groups.dag).toEqual([issuesSample[1]]);
  });

  it('uses stepName when stepPath is missing', () => {
    const a = issue({ stepName: 'fetch' });
    const groups = groupIssues(modelArraySteps, [a], namesArraySteps);
    expect(groups.byStep).toEqual({ 'steps[0]': [a] });
    expect(groups.dag).toEqual([]);
  });

  it('uses stepName when stepPath is unknown', () => {
    const a = issue({ stepPath: 'steps[9]', stepName: 'publish' });
    const groups = groupIssues(modelArraySteps, [a], namesArraySteps);
    expect(groups.byStep).toEqual({ 'steps[2]': [a] });
  });

  it('sends an unknown step to dag', () => {
    const a = issue({ stepPath: 'steps[9]', stepName: 'nope' });
    const b = issue({ stepName: 'toString' });
    const groups = groupIssues(modelArraySteps, [a, b], namesArraySteps);
    expect(groups.byStep).toEqual({});
    expect(groups.dag).toEqual([a, b]);
  });

  it('ignores a name that maps outside the model', () => {
    const a = issue({ stepName: 'ghost' });
    const names = {
      byKey: namesArraySteps.byKey,
      byName: { ...namesArraySteps.byName, ghost: 'steps[7]' },
    };
    const groups = groupIssues(modelArraySteps, [a], names);
    expect(groups.dag).toEqual([a]);
  });

  it('groups handler keys', () => {
    const handler = {
      key: 'handler_on.failure',
      index: null,
      handler: 'failure',
      form: 'block',
      kind: 'run',
      depends: [],
      value: {},
      range: {
        from: 0,
        to: 1,
        startLine: 1,
        startColumn: 1,
        endLine: 1,
        endColumn: 2,
      },
    } as StepView;
    const model: DagDocModel = { ...modelArraySteps, handlers: [handler] };
    const a = issue({ stepPath: 'handler_on.failure' });
    const b = issue({ stepPath: 'handler_on.exit' });
    const groups = groupIssues(model, [a, b], namesArraySteps);
    expect(groups.byStep).toEqual({ 'handler_on.failure': [a] });
    expect(groups.dag).toEqual([b]);
  });

  it('keeps the input order in a group', () => {
    const a = issue({ stepPath: 'steps[0]', message: 'a' });
    const b = issue({ stepName: 'fetch', message: 'b' });
    const c = issue({ stepPath: 'steps[0]', message: 'c' });
    const groups = groupIssues(modelArraySteps, [a, b, c], namesArraySteps);
    expect(groups.byStep['steps[0]']).toEqual([a, b, c]);
  });
});

describe('rangeFromOffsets', () => {
  it('matches the range of a fixture step', () => {
    const step = modelArraySteps.steps[1];
    expect(step).toBeDefined();
    const { range } = step as StepView;
    expect(
      rangeFromOffsets(modelArraySteps.text, range.from, range.to)
    ).toEqual(range);
  });

  it('counts lines and columns from 1', () => {
    expect(rangeFromOffsets('ab\ncd\n', 3, 5)).toEqual({
      from: 3,
      to: 5,
      startLine: 2,
      startColumn: 1,
      endLine: 2,
      endColumn: 3,
    });
  });

  it('puts a line end on the line that it ends', () => {
    expect(rangeFromOffsets('ab\ncd', 1, 2)).toEqual({
      from: 1,
      to: 2,
      startLine: 1,
      startColumn: 2,
      endLine: 1,
      endColumn: 3,
    });
  });

  it('spans lines', () => {
    const r = rangeFromOffsets('ab\ncd\nef', 1, 7);
    expect([r.startLine, r.startColumn, r.endLine, r.endColumn]).toEqual([
      1, 2, 3, 2,
    ]);
  });

  it('counts lines and columns in CRLF text', () => {
    const r = rangeFromOffsets('ab\r\ncd\r\nef', 4, 6);
    expect([r.startLine, r.startColumn, r.endLine, r.endColumn]).toEqual([
      2, 1, 2, 3,
    ]);
  });

  it('counts UTF-16 units', () => {
    // The emoji takes two UTF-16 units.
    const r = rangeFromOffsets('a\u{1F600}b', 3, 4);
    expect([r.startColumn, r.endColumn]).toEqual([4, 5]);
  });

  it('clamps to the text', () => {
    expect(rangeFromOffsets('ab', -4, 99)).toEqual({
      from: 0,
      to: 2,
      startLine: 1,
      startColumn: 1,
      endLine: 1,
      endColumn: 3,
    });
    expect(rangeFromOffsets('ab', 2, 1).to).toBe(2);
  });
});

const noNames: StepNameMap = { byKey: {}, byName: {} };

function slice(text: string, r: TextRange | null): string | null {
  return r ? text.slice(r.from, r.to) : null;
}

// Builds a step view that covers `first` up to the end of `last`.
function viewOf(
  text: string,
  key: string,
  first: string,
  last: string,
  handler?: StepView['handler']
): StepView {
  const from = text.indexOf(first);
  const to = text.indexOf(last, from) + last.length;
  return {
    key,
    index: handler ? null : Number(/\d+/.exec(key)?.[0]),
    handler,
    form: 'block',
    kind: 'run',
    depends: [],
    value: {},
    range: rangeFromOffsets(text, from, to),
  };
}

function modelOf(
  text: string,
  steps: StepView[],
  handlers: StepView[] = []
): DagDocModel {
  return {
    text,
    ok: true,
    parseErrors: [],
    documentCount: 1,
    dagType: 'graph',
    stepsForm: 'array',
    steps,
    handlers,
  };
}

const httpJqModel = modelOf(yamlHttpJq, [
  viewOf(yamlHttpJq, 'steps[0]', 'id: fetch', 'url: ${params.API_URL}'),
  viewOf(yamlHttpJq, 'steps[1]', 'id: names', '${steps.fetch.outputs.body}'),
]);

describe('locateIssue: line', () => {
  it('uses line and column to the end of the line', () => {
    const r = locateIssue(modelArraySteps, issuesSample[0]!, namesArraySteps);
    const at = modelArraySteps.text.indexOf('depends: [fetch]');
    expect(r).toEqual({
      from: at,
      to: at + 16,
      startLine: 9,
      startColumn: 5,
      endLine: 9,
      endColumn: 21,
    });
    expect(slice(modelArraySteps.text, r)).toBe('depends: [fetch]');
  });

  it('defaults the column to 1', () => {
    const r = locateIssue(modelArraySteps, issue({ line: 3 }), namesArraySteps);
    expect(slice(modelArraySteps.text, r)).toBe('steps:');
    expect(r?.startColumn).toBe(1);
  });

  it('keeps the carriage return out of a line of CRLF text', () => {
    const text = modelArraySteps.text.replace(/\n/g, '\r\n');
    const r = locateIssue(modelOf(text, []), issue({ line: 3 }), noNames);
    expect(slice(text, r)).toBe('steps:');
    expect(r).toMatchObject({ startLine: 3, startColumn: 1, endLine: 3 });
  });

  it('clamps the column to the line', () => {
    const r = locateIssue(
      modelArraySteps,
      issue({ line: 3, column: 99 }),
      namesArraySteps
    );
    expect(r).toMatchObject({ startLine: 3, startColumn: 7, endColumn: 7 });
    expect(r?.from).toBe(r?.to);
  });

  it('skips a line beyond the text', () => {
    const text = modelArraySteps.text;
    const a = issue({ line: 99, fieldPath: 'steps[1].depends' });
    expect(slice(text, locateIssue(modelArraySteps, a, namesArraySteps))).toBe(
      '[fetch]'
    );
    const b = issue({ line: 99, stepName: 'publish' });
    expect(locateIssue(modelArraySteps, b, namesArraySteps)).toEqual(
      modelArraySteps.steps[2]?.range
    );
    expect(
      locateIssue(modelArraySteps, issue({ line: 99 }), noNames)
    ).toBeNull();
  });

  it('does not open a line after the last line break', () => {
    const lines = modelArraySteps.text.split('\n').length;
    expect(
      locateIssue(modelArraySteps, issue({ line: lines }), noNames)
    ).toBeNull();
    expect(
      locateIssue(modelArraySteps, issue({ line: lines - 1 }), noNames)
    ).not.toBeNull();
  });
});

describe('locateIssue: fieldPath', () => {
  it('finds with.url', () => {
    const r = locateIssue(
      httpJqModel,
      issue({ fieldPath: 'steps[0].with.url' }),
      noNames
    );
    expect(slice(yamlHttpJq, r)).toBe('${params.API_URL}');
    expect(r).toMatchObject({ startLine: 10, startColumn: 12, endLine: 10 });
  });

  it('finds a value of the second step', () => {
    const r = locateIssue(
      httpJqModel,
      issue({ fieldPath: 'steps[1].with.filter' }),
      noNames
    );
    expect(slice(yamlHttpJq, r)).toBe("'[.[].name]'");
  });

  it('finds a sequence index at DAG level', () => {
    const r = locateIssue(
      httpJqModel,
      issue({ fieldPath: 'params[0]' }),
      noNames
    );
    expect(slice(yamlHttpJq, r)).toBe('API_URL: https://api.example.com/items');
    expect(r?.startLine).toBe(3);
    const key = locateIssue(
      httpJqModel,
      issue({ fieldPath: 'params[0].API_URL' }),
      noNames
    );
    expect(slice(yamlHttpJq, key)).toBe('https://api.example.com/items');
  });

  it('finds a whole step', () => {
    const r = locateIssue(
      httpJqModel,
      issue({ fieldPath: 'steps[1]' }),
      noNames
    );
    expect(r).toEqual(httpJqModel.steps[1]?.range);
  });

  it('returns the deepest node of a partial path', () => {
    const r = locateIssue(
      httpJqModel,
      issue({ fieldPath: 'steps[0].with.nope.deeper' }),
      noNames
    );
    expect(slice(yamlHttpJq, r)).toBe(
      'method: GET\n      url: ${params.API_URL}'
    );
  });

  it('falls back to the step when nothing is found', () => {
    const r = locateIssue(
      httpJqModel,
      issue({ fieldPath: 'steps[7].with.url', stepPath: 'steps[1]' }),
      noNames
    );
    expect(r).toEqual(httpJqModel.steps[1]?.range);
    const s = locateIssue(
      modelArraySteps,
      issue({ fieldPath: 'consts.image', stepName: 'fetch' }),
      namesArraySteps
    );
    expect(s).toEqual(modelArraySteps.steps[0]?.range);
  });

  it('counts UTF-16 units before the target', () => {
    const text = 'env: {A: "\u{1F600} \u00e9", B: x}\nsteps:\n  - run: echo\n';
    const model = modelOf(text, [
      viewOf(text, 'steps[0]', 'run: echo', 'echo'),
    ]);
    const r = locateIssue(model, issue({ fieldPath: 'env.B' }), noNames);
    const at = text.indexOf('x}');
    expect(r).toEqual({
      from: at,
      to: at + 1,
      startLine: 1,
      startColumn: at + 1,
      endLine: 1,
      endColumn: at + 2,
    });
    expect(slice(text, r)).toBe('x');
  });

  it('finds a key that has no value', () => {
    const text = 'name: n\nenv:\nsteps:\n  - run: a\n';
    const model = modelOf(text, [viewOf(text, 'steps[0]', 'run: a', 'a\n')]);
    const r = locateIssue(model, issue({ fieldPath: 'env' }), noNames);
    expect(slice(text, r)).toBe('env');
  });
});

describe('locateIssue: map-form steps', () => {
  const text =
    'name: m\nsteps:\n  zeta:\n    run: echo z\n  alpha:\n    run: echo a\n    with:\n      x: 1\n';
  // Server order is by name: steps[0] is the second entry of the file.
  const steps = [
    viewOf(text, 'steps[0]', 'run: echo a', 'x: 1'),
    viewOf(text, 'steps[1]', 'run: echo z', 'echo z'),
  ];
  const model = { ...modelOf(text, steps), stepsForm: 'map' as const };

  it('uses server numbering', () => {
    const a = locateIssue(model, issue({ fieldPath: 'steps[0].run' }), noNames);
    expect(slice(text, a)).toBe('echo a');
    const z = locateIssue(model, issue({ fieldPath: 'steps[1].run' }), noNames);
    expect(slice(text, z)).toBe('echo z');
    expect(z?.startLine).toBe(4);
  });

  it('accepts a range that starts at the key', () => {
    const keyed = {
      ...model,
      steps: [
        viewOf(text, 'steps[0]', 'alpha:', 'x: 1'),
        viewOf(text, 'steps[1]', 'zeta:', 'echo z'),
      ],
    };
    const r = locateIssue(
      keyed,
      issue({ fieldPath: 'steps[0].with.x' }),
      noNames
    );
    expect(slice(text, r)).toBe('1');
    const whole = locateIssue(keyed, issue({ fieldPath: 'steps[1]' }), noNames);
    expect(slice(text, whole)).toBe('run: echo z');
  });
});

describe('locateIssue: handlers', () => {
  const model = modelOf(
    yamlHandlers,
    [viewOf(yamlHandlers, 'steps[0]', 'run: ./job.sh', './job.sh')],
    [
      viewOf(
        yamlHandlers,
        'handler_on.success',
        'run: echo ok',
        'echo ok',
        'success'
      ),
      viewOf(
        yamlHandlers,
        'handler_on.failure',
        'run: ./notify.sh',
        './notify.sh',
        'failure'
      ),
    ]
  );

  it('finds a field of a handler', () => {
    const r = locateIssue(
      model,
      issue({ fieldPath: 'handler_on.failure.run' }),
      noNames
    );
    expect(slice(yamlHandlers, r)).toBe('./notify.sh');
    expect(r?.startLine).toBe(7);
  });

  it('falls back to the handler range', () => {
    const r = locateIssue(
      model,
      issue({ stepPath: 'handler_on.success' }),
      noNames
    );
    expect(r).toEqual(model.handlers[0]?.range);
  });
});

describe('locateIssue: step range and null', () => {
  it('uses stepPath', () => {
    const r = locateIssue(
      modelArraySteps,
      issue({ stepPath: 'steps[1]' }),
      namesArraySteps
    );
    expect(r).toEqual(modelArraySteps.steps[1]?.range);
  });

  it('uses stepName through names', () => {
    const r = locateIssue(
      modelArraySteps,
      issue({ stepName: 'publish' }),
      namesArraySteps
    );
    expect(r).toEqual(modelArraySteps.steps[2]?.range);
  });

  it('returns null without a place', () => {
    expect(
      locateIssue(modelArraySteps, issuesSample[1]!, namesArraySteps)
    ).toBeNull();
    expect(
      locateIssue(
        modelArraySteps,
        issue({ stepName: 'nope', stepPath: 'steps[9]' }),
        namesArraySteps
      )
    ).toBeNull();
  });
});

describe('attributeIssues', () => {
  const empty = issue({
    message: 'failed to build: step command is empty (value: )',
  });
  const text = [
    'steps:',
    '  - name: first',
    '    run: echo hi',
    '  - name: second',
    '    run: ""',
    '  - name: third',
    '    run: ""',
    '',
  ].join('\n');

  it('gives the first step with an empty command to an error without a step', () => {
    const [result] = attributeIssues(parseDagDoc(text), [empty]);
    expect(result).toMatchObject({
      stepName: 'second',
      stepPath: 'steps[1]',
    });
  });

  it('keeps the issues that have a step and the other messages', () => {
    const named = issue({
      message: 'step command is empty',
      stepName: 'third',
    });
    const other = issue({ message: 'invalid keys: x' });
    const issues = [named, other];
    expect(attributeIssues(parseDagDoc(text), issues)).toEqual(issues);
  });

  it('changes nothing when no step has an empty command', () => {
    const issues = [empty];
    expect(
      attributeIssues(
        parseDagDoc('steps:\n  - name: a\n    run: echo\n'),
        issues
      )
    ).toEqual(issues);
  });
});
