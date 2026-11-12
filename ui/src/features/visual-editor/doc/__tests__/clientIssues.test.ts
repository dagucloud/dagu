// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  ReferenceScopeStepOutputsKnown,
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { ReferenceScope, ValidationIssue } from '../../contracts';
import { realSchema } from '../../inspector/__tests__/realSchema';
import {
  collectClientIssues,
  issueLine,
  mergeIssues,
  type ClientIssueInput,
} from '../clientIssues';
import { parseDagDocLenient, resolveStepNames } from '../parse';

// The file of the issues demo: the server stops at the first error of it.
const fixture = `# Demo: fetch JSON over HTTP, filter it with jq, log the result.
description: Fetch a JSON document, extract its title with jq and log it.
schedule:
  - 0 9 * * *
  - 0 9 * * 1-5
steps:
  - id: fetch
    name: fetch
    action: http.request
    with:
      method: GET
      
      format: json
      silent: true
    output:
      body:
        from: stdout
        decode: json
        select: .body

  - id: title
    name: extract title
    depends: [fetch]
    action: ""
 
   
  - name: show title
    depends: [title]
    action: log.write
    with:
      message: "Title: \${steps.title.outputs.text}"
`;

function input(
  text: string,
  extra: Partial<ClientIssueInput> = {}
): ClientIssueInput {
  const model = parseDagDocLenient(text);
  return {
    model,
    schema: realSchema,
    names: resolveStepNames(model),
    ...extra,
  };
}

function issuesOf(text: string, extra: Partial<ClientIssueInput> = {}) {
  return collectClientIssues(input(text, extra));
}

const serverIssue = (extra: Partial<ValidationIssue>): ValidationIssue => ({
  severity: ValidationIssueSeverity.error,
  source: ValidationIssueSource.build,
  message: 'problem',
  ...extra,
});

const lineOf = (text: string, needle: string) =>
  text.split('\n').findIndex((line) => line.includes(needle)) + 1;

describe.skipIf(realSchema === null)(
  'collectClientIssues on the demo file',
  () => {
    const issues = issuesOf(fixture);
    const of = (stepPath: string) =>
      issues.filter((issue) => issue.stepPath === stepPath);

    it('finds the missing url of the request', () => {
      const url = of('steps[0]').find(
        (i) => i.fieldPath === 'steps[0].with.url'
      );
      expect(url).toMatchObject({
        severity: ValidationIssueSeverity.error,
        message: 'url is required.',
        stepName: 'fetch',
      });
      // In the `with` block: the deepest node that exists.
      expect(url?.line).toBe(lineOf(fixture, 'method: GET'));
    });

    it('finds the empty action of the step that extracts the title', () => {
      expect(of('steps[1]')).toEqual([
        expect.objectContaining({
          severity: ValidationIssueSeverity.error,
          message: 'Action is empty.',
          stepName: 'extract title',
          fieldPath: 'steps[1].action',
          line: lineOf(fixture, 'action: ""'),
        }),
      ]);
    });

    it('warns about the step without id when the other steps have one', () => {
      expect(of('steps[2]')).toContainEqual(
        expect.objectContaining({
          severity: ValidationIssueSeverity.warning,
          message: 'The step has no id, as the other steps have.',
          stepName: 'show title',
        })
      );
    });

    it('finds the reference to an output that the step does not have', () => {
      const reference = of('steps[2]').find((i) =>
        i.message.includes('output')
      );
      expect(reference).toMatchObject({
        severity: ValidationIssueSeverity.error,
        message: 'The step "title" has no output "text".',
        line: lineOf(fixture, 'Title: ${steps.title.outputs.text}'),
      });
    });

    it('finds every issue in one pass', () => {
      expect(issues.filter((i) => i.severity === 'error')).toHaveLength(3);
    });

    it('warns about a schedule that is no cron expression', () => {
      const text = fixture.replace('0 9 * * 1-5', 'every day');
      const bad = issuesOf(text).find((i) => i.message.includes('every day'));
      expect(bad).toMatchObject({
        severity: ValidationIssueSeverity.warning,
        line: lineOf(text, 'every day'),
      });
      expect(issues.some((i) => i.message.includes('schedule'))).toBe(false);
    });
  }
);

describe.skipIf(realSchema === null)('collectClientIssues rules', () => {
  const step = (body: string) => `steps:\n${body}`;

  it('reports a wrong type of a field', () => {
    const issues = issuesOf(
      step('  - name: a\n    run: echo\n    timeout_sec: soon\n')
    );
    expect(issues).toContainEqual(
      expect.objectContaining({
        severity: ValidationIssueSeverity.error,
        fieldPath: 'steps[0].timeout_sec',
        line: 4,
      })
    );
  });

  it('accepts a reference where the schema wants a number', () => {
    expect(
      issuesOf(
        step('  - name: a\n    run: echo\n    timeout_sec: ${TIMEOUT}\n')
      )
    ).toEqual([]);
  });

  it('warns about an unknown field', () => {
    const issues = issuesOf(
      step('  - name: a\n    run: echo\n    colour: red\n')
    );
    expect(issues).toContainEqual(
      expect.objectContaining({
        severity: ValidationIssueSeverity.warning,
        message: 'Unknown field "colour".',
      })
    );
  });

  it('reports an unknown action, and accepts builtin and custom ones', () => {
    const text = step(
      '  - name: a\n    action: nothing.here\n  - name: b\n    action: log.write\n    with: {message: hi}\n  - name: c\n    action: mine\n'
    );
    const issues = issuesOf(text, { customActions: ['mine'] });
    expect(issues).toEqual([
      expect.objectContaining({
        message: 'Unknown action "nothing.here".',
        stepPath: 'steps[0]',
        line: 3,
      }),
    ]);
  });

  it('accepts an action that the file declares itself', () => {
    const text = `actions:\n  mine:\n    run: echo\nsteps:\n  - name: a\n    action: mine\n`;
    expect(issuesOf(text)).toEqual([]);
  });

  it('warns about a step with no action and no command', () => {
    const issues = issuesOf(step('  - name: a\n    depends: []\n'));
    expect(issues).toEqual([
      expect.objectContaining({
        severity: ValidationIssueSeverity.warning,
        message: 'The step has no action and no command.',
        line: 2,
      }),
    ]);
  });

  it('reports duplicate names and duplicate ids', () => {
    const issues = issuesOf(
      step(
        '  - name: a\n    id: x\n    run: echo\n  - name: a\n    id: x\n    run: echo\n'
      )
    );
    const messages = issues.map((i) => i.message);
    expect(messages).toContain('Duplicate step name "a".');
    expect(messages).toContain('Duplicate step ID "x".');
  });

  it('reports a step ID that is not valid', () => {
    const issues = issuesOf(step('  - name: a\n    id: 1x\n    run: echo\n'));
    expect(issues[0]).toMatchObject({
      severity: ValidationIssueSeverity.error,
      fieldPath: 'steps[0].id',
    });
  });

  it('requires an id for a step that declares outputs', () => {
    const issues = issuesOf(
      step(
        '  - name: a\n    run: echo\n    outputs:\n      x:\n        from: stdout\n'
      )
    );
    expect(issues.map((i) => i.message)).toContain(
      'id is required when the step declares outputs.'
    );
  });

  it('reports a dependency on a step that does not exist', () => {
    const issues = issuesOf(
      step(
        '  - name: a\n    run: echo\n    depends: [ghost]\n  - name: b\n    run: echo\n    depends: [a]\n'
      )
    );
    expect(issues).toEqual([
      expect.objectContaining({
        message: 'Depends on the unknown step "ghost".',
        stepPath: 'steps[0]',
        line: 4,
      }),
    ]);
  });

  // A step without name and id gets `<type>_<n>` from the server.
  it('accepts the name that the server gives an unnamed step', () => {
    const issues = issuesOf(
      step(
        '  - action: http.request\n    with: {method: GET, url: "http://x"}\n  - run: echo\n  - name: b\n    run: echo\n    depends: [http_1, cmd_2]\n'
      )
    );
    expect(issues).toEqual([]);
  });

  // A handler takes the name of its event, so its name: is not checked.
  it('accepts the same name in two handlers', () => {
    const issues = issuesOf(
      'handler_on:\n  success:\n    name: cleanup\n    run: echo\n  failure:\n    name: cleanup\n    run: echo\nsteps:\n  - name: cleanup\n    run: echo\n'
    );
    expect(issues).toEqual([]);
  });

  it('reports a reference to a step that does not exist', () => {
    const issues = issuesOf(
      step('  - name: a\n    run: echo ${steps.ghost.outputs.x}\n')
    );
    expect(issues[0]).toMatchObject({
      message: expect.stringContaining('unknown step "ghost"'),
      stepPath: 'steps[0]',
      line: 3,
    });
  });

  it('ignores a reference in a YAML comment', () => {
    const issues = issuesOf(
      step(
        '  - name: a\n    # ${steps.ghost.outputs.x}\n    run: echo # ${steps.ghost.outputs.y}\n'
      )
    );
    expect(issues).toEqual([]);
  });

  it('accepts a reference to a step of the same foreach body', () => {
    const text = step(
      [
        '  - id: list',
        '    run: ./list.sh',
        '  - id: summarize_all',
        '    depends: list',
        '    foreach:',
        '      items: ${steps.list.outputs.items}',
        '      steps:',
        '        - id: fetch',
        '          run: ./fetch.sh',
        '          outputs:',
        '            - name: path',
        '        - id: summarize',
        '          depends: fetch',
        '          run: ./summarize.sh ${steps.fetch.outputs.path}',
        '          outputs:',
        '            - name: markdown',
        '      collect:',
        '        markdown: ${steps.summarize.outputs.markdown}',
        '',
      ].join('\n')
    );
    expect(
      issuesOf(text).filter((i) => i.message.includes('unknown step'))
    ).toEqual([]);
  });

  // The server says what the steps give; the text only says what a step with
  // no execution gives.
  it('checks the outputs of the server scope when it has one', () => {
    const scope = {
      params: [],
      consts: [],
      env: [],
      secrets: [],
      steps: [
        {
          name: 'fetch',
          id: 'fetch',
          stepPath: 'steps[0]',
          upstream: [],
          outputsKnown: ReferenceScopeStepOutputsKnown.static,
          outputs: [
            {
              name: 'body',
              reference: '${steps.fetch.outputs.body}',
              source: 'action',
            },
          ],
        },
      ],
    } as unknown as ReferenceScope;
    const text = step(
      '  - id: fetch\n    action: http.request\n    with: {method: GET, url: "http://x"}\n  - id: use\n    depends: [fetch]\n    run: echo ${steps.fetch.outputs.body} ${steps.fetch.outputs.nope}\n'
    );
    const issues = issuesOf(text, { scope });
    expect(issues.map((i) => i.message)).toEqual([
      'The step "fetch" has no output "nope".',
    ]);
  });

  it('leaves a step that has a YAML error to the parser', () => {
    const text =
      'steps:\n  - name: a\n    run: [broken\n  - name: b\n    run: echo\n';
    expect(issuesOf(text).filter((i) => i.stepPath === 'steps[0]')).toEqual([]);
  });

  it('gives no issue for a text that does not parse', () => {
    expect(issuesOf('\tname: x')).toEqual([]);
  });
});

describe.skipIf(realSchema === null)('mergeIssues', () => {
  const model = parseDagDocLenient(fixture);
  const names = resolveStepNames(model);
  const client = collectClientIssues(input(fixture));

  it('merges one server error and the client errors without duplicates', () => {
    // The server stops at the first error: the missing field of the request.
    const server = [
      serverIssue({
        message:
          "field 'steps': field 'with': with.url is required (value: map[method:GET])",
        stepName: 'fetch',
        stepPath: 'steps[0]',
        fieldPath: 'steps[0].with',
      }),
    ];
    const merged = mergeIssues(model, names, server, client);

    // The missing url is said once, with the text of the server.
    const urls = merged.filter(
      (i) =>
        i.fieldPath?.endsWith('with.url') || i.fieldPath === 'steps[0].with'
    );
    expect(urls).toHaveLength(1);
    expect(urls[0]).toBe(server[0]);
    expect(merged.length).toBe(client.length);
    expect(merged.filter((i) => i.severity === 'error')).toHaveLength(3);
  });

  it('counts an empty command of the server and an empty action as one issue', () => {
    const server = [
      serverIssue({
        message: 'step command is empty',
        stepName: 'extract title',
        stepPath: 'steps[1]',
      }),
    ];
    const merged = mergeIssues(model, names, server, client);
    expect(
      merged.filter((i) => i.stepPath === 'steps[1]' && i.severity === 'error')
    ).toEqual(server);
  });

  it('keeps a server issue that the client did not find', () => {
    const server = [
      serverIssue({ message: 'a rule of the server', stepPath: 'steps[0]' }),
    ];
    expect(mergeIssues(model, names, server, client)).toContain(server[0]);
  });

  it('drops a client reference that the server has said as a value reference', () => {
    const server = [
      serverIssue({
        severity: ValidationIssueSeverity.notice,
        source: ValidationIssueSource.valueReference,
        message: 'Step id title does not declare the output text',
        stepPath: 'steps[2]',
        stepName: 'show title',
      }),
    ];
    const merged = mergeIssues(model, names, server, client);
    expect(
      merged.filter((i) => i.message.startsWith('The step "title"'))
    ).toEqual([]);
  });

  it('sorts by line, errors first on one line, the whole file first', () => {
    const server = [
      serverIssue({ message: 'whole file' }),
      serverIssue({
        severity: ValidationIssueSeverity.warning,
        source: ValidationIssueSource.warning,
        message: 'late',
        stepPath: 'steps[2]',
      }),
    ];
    const merged = mergeIssues(model, names, server, client);
    expect(merged[0]?.message).toBe('whole file');
    const lines = merged.map((i) => issueLine(model, names, i) ?? 0);
    expect(lines).toEqual([...lines].sort((a, b) => a - b));
  });

  it('gives the server list back when the client has nothing to add', () => {
    const server = [serverIssue({ message: 'm' })];
    expect(mergeIssues(model, names, server, [])).toBe(server);
  });
});
