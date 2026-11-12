// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { ValidationIssue } from '../../contracts';
import { NO_ID_MESSAGE, OUTPUTS_ID_MESSAGE } from '../issueMessages';
import { locateIssue } from '../issues';
import { parseDagDoc, resolveStepNames } from '../parse';
import { applyTextEdits } from '../patch';
import {
  mergeQuickFixes,
  planQuickFixes,
  planQuickFixesInRange,
  quickFixesForMarker,
  slugStepId,
  uniqueStepId,
} from '../quickFixes';

function issueOf(
  stepPath: string,
  message = OUTPUTS_ID_MESSAGE,
  severity: ValidationIssue['severity'] = ValidationIssueSeverity.error
): ValidationIssue {
  return {
    severity,
    source: ValidationIssueSource.build,
    message,
    stepPath,
  };
}

/** Applies the fixes of the issues to the text, as the Fix all button does. */
function fixAll(text: string, issues: ValidationIssue[]): string {
  const plan = planQuickFixes(parseDagDoc(text), issues);
  return applyTextEdits(text, mergeQuickFixes(plan.values()));
}

describe('slugStepId', () => {
  it.each([
    ['show title', 'show_title'],
    ['Show Title', 'Show_Title'],
    ['  show   title  ', 'show_title'],
    ['fetch-data.v2', 'fetch_data_v2'],
    ['Étape numéro 1', 'Etape_numero_1'],
    ['crème brûlée!', 'creme_brulee'],
    ['a/b\\c: d?', 'a_b_c_d'],
    ['1st step', 'step_1st_step'],
    ['output', 'step_output'],
    ['!!!', ''],
    ['日本語', ''],
    ['', ''],
  ])('turns %j into %j', (name, id) => {
    expect(slugStepId(name)).toBe(id);
  });

  it('keeps an id inside the limit of 40 characters', () => {
    expect(slugStepId('x'.repeat(100)).length).toBeLessThanOrEqual(35);
  });
});

describe('uniqueStepId', () => {
  it('takes the slug, then _1, _2, and so on', () => {
    expect(uniqueStepId('show_title', 1, new Set())).toBe('show_title');
    expect(uniqueStepId('show_title', 1, new Set(['show_title']))).toBe(
      'show_title_1'
    );
    expect(
      uniqueStepId(
        'show_title',
        1,
        new Set(['show_title', 'show_title_1', 'show_title_2'])
      )
    ).toBe('show_title_3');
  });

  it('names a step without a readable name by its position', () => {
    expect(uniqueStepId('', 3, new Set())).toBe('step_3');
    expect(uniqueStepId('', 3, new Set(['step_3']))).toBe('step_3_1');
  });
});

describe('planQuickFixes', () => {
  it('inserts the id line after the name line, nothing else changes', () => {
    const text =
      'steps:\n  - name: show title\n    run: echo hi\n  - name: b\n    run: echo\n';
    const model = parseDagDoc(text);
    const plan = planQuickFixes(model, [issueOf('steps[0]')]);
    const [fix] = [...plan.values()];
    expect(fix?.message).toBe('Add id: {id}');
    expect(fix?.params).toEqual({ id: 'show_title' });
    // One hunk: an insertion, with the indentation of the keys.
    expect(fix?.edits).toEqual([
      {
        from: text.indexOf('\n    run: echo hi'),
        to: text.indexOf('\n    run: echo hi'),
        insert: '\n    id: show_title',
      },
    ]);
    expect(applyTextEdits(text, fix!.edits)).toBe(
      'steps:\n  - name: show title\n    id: show_title\n    run: echo hi\n  - name: b\n    run: echo\n'
    );
  });

  it('puts the id after the name line when name is not the first key', () => {
    const text =
      'steps:\n  - run: echo hi\n    name: say hi # greet\n    depends: []\n';
    expect(fixAll(text, [issueOf('steps[0]')])).toBe(
      'steps:\n  - run: echo hi\n    name: say hi # greet\n    id: say_hi\n    depends: []\n'
    );
  });

  it('keeps the indentation of a list that is not indented', () => {
    const text = 'steps:\n- name: a b\n  run: echo\n';
    expect(fixAll(text, [issueOf('steps[0]')])).toBe(
      'steps:\n- name: a b\n  id: a_b\n  run: echo\n'
    );
  });

  it('inserts at the end of a file without a final line break', () => {
    const text = 'steps:\n  - run: echo\n    name: last';
    expect(fixAll(text, [issueOf('steps[0]')])).toBe(
      'steps:\n  - run: echo\n    name: last\n    id: last'
    );
  });

  it('keeps the line breaks of a CRLF file', () => {
    const text = 'steps:\r\n  - name: a b\r\n    run: echo\r\n';
    expect(fixAll(text, [issueOf('steps[0]')])).toBe(
      'steps:\r\n  - name: a b\r\n    id: a_b\r\n    run: echo\r\n'
    );
  });

  it('does not touch a name key that sits deeper in the step', () => {
    const text =
      'steps:\n  - name: call\n    action: http.request\n    with:\n      name: inner\n';
    expect(fixAll(text, [issueOf('steps[0]')])).toBe(
      'steps:\n  - name: call\n    id: call\n    action: http.request\n    with:\n      name: inner\n'
    );
  });

  it('gives no fix to a step that has an id already', () => {
    const text = 'steps:\n  - name: a\n    id: a\n    run: echo\n';
    expect(planQuickFixes(parseDagDoc(text), [issueOf('steps[0]')]).size).toBe(
      0
    );
  });

  it('gives no fix for a step with no name line, for a shorthand and for other issues', () => {
    const noName = 'steps:\n  - run: echo\n    outputs:\n      x: y\n';
    expect(
      planQuickFixes(parseDagDoc(noName), [issueOf('steps[0]')]).size
    ).toBe(0);
    const shorthand = 'steps:\n  - echo hi\n';
    expect(
      planQuickFixes(parseDagDoc(shorthand), [issueOf('steps[0]')]).size
    ).toBe(0);
    const text = 'steps:\n  - name: a\n    run: echo\n';
    expect(
      planQuickFixes(parseDagDoc(text), [
        issueOf('steps[0]', 'Action is empty.'),
      ]).size
    ).toBe(0);
  });

  it('answers the warning, the error and the message of the server alike', () => {
    const text =
      'steps:\n  - name: a\n    run: echo\n  - name: b\n    run: echo\n  - name: c\n    run: echo\n';
    const plan = planQuickFixes(parseDagDoc(text), [
      issueOf('steps[0]', NO_ID_MESSAGE, ValidationIssueSeverity.warning),
      issueOf('steps[1]', OUTPUTS_ID_MESSAGE),
      issueOf('steps[2]', 'action human.task requires an explicit id'),
    ]);
    expect(plan.size).toBe(3);
  });

  it('finds the step by its name when the path is missing', () => {
    const text = 'steps:\n  - name: a\n    run: echo\n';
    const plan = planQuickFixes(parseDagDoc(text), [
      {
        severity: ValidationIssueSeverity.error,
        source: ValidationIssueSource.build,
        message: OUTPUTS_ID_MESSAGE,
        stepName: 'a',
      },
    ]);
    expect(plan.size).toBe(1);
  });

  it('serves the warning and the error of one step with one fix', () => {
    const text =
      'steps:\n  - name: a\n    id: x\n    run: echo\n  - name: b\n    run: echo\n    outputs:\n      k: v\n';
    const plan = planQuickFixes(parseDagDoc(text), [
      issueOf('steps[1]', NO_ID_MESSAGE, ValidationIssueSeverity.warning),
      issueOf('steps[1]'),
    ]);
    expect(plan.size).toBe(2);
    expect(new Set(plan.values()).size).toBe(1);
    expect(mergeQuickFixes(plan.values())).toHaveLength(1);
  });

  it('avoids an id that exists as an id or as the name of another step', () => {
    const text =
      'steps:\n  - name: show title\n    run: echo\n  - name: x\n    id: show_title\n    run: echo\n  - name: show_title_1\n    run: echo\n';
    const [fix] = [
      ...planQuickFixes(parseDagDoc(text), [issueOf('steps[0]')]).values(),
    ];
    expect(fix?.params).toEqual({ id: 'show_title_2' });
  });

  it('lets a step take its own name as its id', () => {
    const text = 'steps:\n  - name: build\n    run: echo\n';
    const [fix] = [
      ...planQuickFixes(parseDagDoc(text), [issueOf('steps[0]')]).values(),
    ];
    expect(fix?.params).toEqual({ id: 'build' });
  });

  it('does not take its own name as its id when another step has it as id', () => {
    const text =
      'steps:\n  - name: build\n    run: echo\n  - name: compile\n    id: build\n    run: echo\n';
    const [fix] = [
      ...planQuickFixes(parseDagDoc(text), [issueOf('steps[0]')]).values(),
    ];
    expect(fix?.params).toEqual({ id: 'build_1' });
  });

  it('gives two steps with one name different ids in one batch', () => {
    // Two steps with one name: the second one is a duplicate, but the fixes
    // must not give them the same id.
    const text =
      'steps:\n  - name: show title\n    run: echo 1\n  - name: show title\n    run: echo 2\n  - name: show title\n    run: echo 3\n';
    const issues = [0, 1, 2].map((n) => issueOf(`steps[${n}]`));
    const next = fixAll(text, issues);
    expect(next).toBe(
      'steps:\n  - name: show title\n    id: show_title\n    run: echo 1\n  - name: show title\n    id: show_title_1\n    run: echo 2\n  - name: show title\n    id: show_title_2\n    run: echo 3\n'
    );
    // The result reads back with three different ids.
    const ids = parseDagDoc(next).steps.map((step) => step.id);
    expect(new Set(ids).size).toBe(3);
  });

  it('merges the hunks in ascending order', () => {
    const text =
      'steps:\n  - name: a\n    run: echo\n  - name: b\n    run: echo\n';
    const plan = planQuickFixes(parseDagDoc(text), [
      issueOf('steps[1]'),
      issueOf('steps[0]'),
    ]);
    const edits = mergeQuickFixes(plan.values());
    expect(edits.map((edit) => edit.from)).toEqual(
      [...edits.map((edit) => edit.from)].sort((a, b) => a - b)
    );
    expect(edits).toHaveLength(2);
  });
});

describe('quickFixesForMarker', () => {
  const text =
    'steps:\n  - name: show title\n    run: echo hi\n  - name: b\n    id: b\n    run: echo\n  - name: c d\n    run: echo\n';
  const model = parseDagDoc(text);
  const names = resolveStepNames(model);

  /** The marker that the editor draws for an issue. */
  function markerOf(issue: ValidationIssue) {
    const range = locateIssue(model, issue, names)!;
    return {
      message: issue.message,
      startLineNumber: range.startLine,
      startColumn: range.startColumn,
    };
  }

  it('gives the fix of the issue that the marker shows', () => {
    const first = issueOf('steps[0]', NO_ID_MESSAGE);
    const third = issueOf('steps[2]', NO_ID_MESSAGE);
    const issues = [first, third];
    expect(quickFixesForMarker(model, issues, markerOf(first))?.params).toEqual(
      { id: 'show_title' }
    );
    expect(quickFixesForMarker(model, issues, markerOf(third))?.params).toEqual(
      { id: 'c_d' }
    );
  });

  it('gives the fix that the plan of the wand gives', () => {
    const issues = [
      issueOf('steps[0]', NO_ID_MESSAGE),
      issueOf('steps[2]', NO_ID_MESSAGE),
    ];
    const plan = planQuickFixes(model, issues);
    for (const issue of issues) {
      expect(quickFixesForMarker(model, issues, markerOf(issue))).toEqual(
        plan.get(issue)
      );
    }
  });

  it('gives nothing for a marker without a fix', () => {
    const fixable = issueOf('steps[0]', NO_ID_MESSAGE);
    const other = issueOf('steps[1]', 'The action is unknown.');
    expect(
      quickFixesForMarker(model, [fixable, other], markerOf(other))
    ).toBeUndefined();
    expect(
      quickFixesForMarker(model, [fixable, other], {
        message: 'something else',
        startLineNumber: 2,
        startColumn: 5,
      })
    ).toBeUndefined();
  });

  it('gives nothing for a marker at another position', () => {
    const issue = issueOf('steps[0]', NO_ID_MESSAGE);
    const marker = { ...markerOf(issue), startLineNumber: 7 };
    expect(quickFixesForMarker(model, [issue], marker)).toBeUndefined();
  });

  it('reads the message without its position prefix', () => {
    const message = 'step requires an explicit id';
    const issue = issueOf('steps[0]', `[2:5] ${message}`);
    const marker = { ...markerOf(issue), message };
    expect(quickFixesForMarker(model, [issue], marker)).toBeDefined();
  });
});

describe('planQuickFixesInRange', () => {
  const text = [
    'name: demo',
    'steps:',
    '  - name: first one',
    '    run: echo 1',
    '',
    '  - name: second one',
    '    run: echo 2',
    '',
    '  - name: third',
    '    id: third',
    '    run: echo 3',
    '',
  ].join('\n');
  const issues = [issueOf('steps[0]'), issueOf('steps[1]')];

  it('keeps the fixes of the step, the same as the whole plan gives', () => {
    const model = parseDagDoc(text);
    const whole = planQuickFixes(model, issues);
    const inside = planQuickFixesInRange(model, issues, model.steps[1]!.range);
    expect(inside.size).toBe(1);
    expect([...inside.values()][0]).toEqual(whole.get(issues[1]!));
    const out = applyTextEdits(text, mergeQuickFixes(inside.values()));
    expect(out).toContain('  - name: second one\n    id: second_one\n');
    // The first step has no id either, but it is out of the range.
    expect(out).not.toContain('id: first_one');
    expect(out.replace('\n    id: second_one', '')).toBe(text);
  });

  it('gives nothing for a step that has no fix', () => {
    const model = parseDagDoc(text);
    expect(
      planQuickFixesInRange(model, issues, model.steps[2]!.range).size
    ).toBe(0);
  });

  it('keeps the edit at the end of the range', () => {
    // The name line is the last line of the step.
    const last = 'steps:\n  - run: echo 1\n    name: only one';
    const model = parseDagDoc(last);
    const only = [issueOf('steps[0]')];
    expect(planQuickFixesInRange(model, only, model.steps[0]!.range).size).toBe(
      1
    );
  });
});
