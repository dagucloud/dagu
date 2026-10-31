// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import { stepRefName } from '../../canvas/gestures';
import { buildCanvasGraph } from '../../canvas/graph';
import type { DagDocModel, ValidationIssue } from '../../contracts';
import { buildActionCatalog } from '../../inspector/catalog';
import { checkStepSchema } from '../../inspector/stepSchemaCheck';
import { realSchema } from '../../inspector/__tests__/realSchema';
import {
  applyEdit,
  attributeIssues,
  createTextHistory,
  groupIssues,
  parseDagDoc,
  parseDagDocLenient,
  resolveStepNames,
} from '..';

// Step 3 has an unclosed flow list: only that entry cannot be read.
const BROKEN_THIRD = `name: five
steps:
  - name: a
    run: echo a
  - name: b
    depends: [a]
    run: echo b
  - name: c
    depends: [b]
    run: [echo, c
  - name: d
    depends: c
    run: echo d
  - name: e
    depends: [d]
    run: echo e
`;

function graphOf(model: DagDocModel, issues: ValidationIssue[] = []) {
  const names = resolveStepNames(model);
  return buildCanvasGraph({
    model,
    names,
    issues: groupIssues(model, attributeIssues(model, issues), names),
  });
}

function edgePairs(model: DagDocModel, issues: ValidationIssue[] = []) {
  return graphOf(model, issues)
    .edges.map((edge) => `${edge.source}>${edge.target}`)
    .sort();
}

describe('one broken step among five', () => {
  const model = parseDagDocLenient(BROKEN_THIRD);

  it('reads the other four steps', () => {
    expect(parseDagDoc(BROKEN_THIRD).ok).toBe(false);
    expect(model.ok).toBe(true);
    expect(model.steps.map((s) => s.explicitName)).toEqual([
      'a',
      'b',
      'c',
      'd',
      'e',
    ]);
    expect(model.steps.map((s) => s.broken !== undefined)).toEqual([
      false,
      false,
      true,
      false,
      false,
    ]);
    expect(model.steps[3]!.value.run).toBe('echo d');
  });

  it('keeps the text and gives the error of the step', () => {
    expect(model.text).toBe(BROKEN_THIRD);
    expect(model.parseErrors).toHaveLength(1);
    expect(model.parseErrors[0]).toMatchObject({ line: 10 });
    expect(model.steps[2]!.broken).toMatch(/flow sequence/i);
    expect(model.sanitizedText).toHaveLength(BROKEN_THIRD.length);
  });

  it('ranges the broken step on its own lines', () => {
    const range = model.steps[2]!.range;
    expect(BROKEN_THIRD.slice(range.from, range.to)).toBe(
      'name: c\n    depends: [b]\n    run: [echo, c'
    );
    expect(range.startLine).toBe(8);
    expect(range.endLine).toBe(10);
    // The next step keeps its own exact range.
    const next = model.steps[3]!.range;
    expect(BROKEN_THIRD.slice(next.from, next.to)).toBe(
      'name: d\n    depends: c\n    run: echo d'
    );
  });

  it('keeps every edge, also those of the placeholder', () => {
    expect(edgePairs(model)).toEqual([
      'steps[0]>steps[1]',
      'steps[1]>steps[2]',
      'steps[2]>steps[3]',
      'steps[3]>steps[4]',
    ]);
  });

  it('draws the placeholder as a node with an error', () => {
    const graph = graphOf(model);
    expect(graph.nodes).toHaveLength(5);
    const node = graph.nodes[2]!;
    expect(node.label).toBe('c');
    expect(node.issues.errors).toBe(1);
    expect(node.issueMessages?.errors[0]).toMatch(/flow sequence/i);
    expect(graph.nodes[3]!.issues.errors).toBe(0);
  });

  it('names a placeholder without a readable name', () => {
    const text = BROKEN_THIRD.replace('name: c', 'name: "c');
    const lost = parseDagDocLenient(text);
    expect(lost.ok).toBe(true);
    expect(lost.steps[2]!.broken).toBeDefined();
    expect(lost.steps[2]!.explicitName).toBeUndefined();
    expect(graphOf(lost).nodes[2]!.label).toBe('Invalid step 3');
    // The label is no name to write in a route.
    expect(stepRefName(graphOf(lost).nodes[2]!)).toBeNull();
    expect(stepRefName(graphOf(model).nodes[2]!)).toBe('c');
    // The depends of the placeholder read; no step can link to its lost name.
    expect(edgePairs(lost)).toEqual([
      'steps[0]>steps[1]',
      'steps[1]>steps[2]',
      'steps[3]>steps[4]',
    ]);
  });
});

describe('text that stays unreadable', () => {
  it('freezes on a tab in the top level keys', () => {
    const text = `name: x\n\tparams: a\nsteps:\n  - name: a\n    run: echo a\n`;
    const model = parseDagDocLenient(text);
    expect(model.ok).toBe(false);
    expect(model.steps).toEqual([]);
  });

  it('freezes on an unclosed quote above the steps', () => {
    const text = `name: "x\nsteps:\n  - name: a\n    run: echo a\n`;
    expect(parseDagDocLenient(text).ok).toBe(false);
  });

  it('freezes when the steps are not a block list', () => {
    const text = `steps: [a, {b: c]\n`;
    expect(parseDagDocLenient(text).ok).toBe(false);
    expect(parseDagDocLenient('just: [').ok).toBe(false);
  });

  it('gives the strict model for a valid text', () => {
    const text = 'steps:\n  - name: a\n    run: echo a\n';
    expect(parseDagDocLenient(text)).toEqual(parseDagDoc(text));
  });
});

describe('a tab in a step', () => {
  const text = `steps:
  - name: a
    run: echo a
  - name: b
\trun: echo b
  - name: c
    run: echo c
`;

  it('breaks that step only', () => {
    const model = parseDagDocLenient(text);
    expect(model.ok).toBe(true);
    expect(model.steps.map((s) => s.broken !== undefined)).toEqual([
      false,
      true,
      false,
    ]);
    expect(model.steps[1]!.explicitName).toBe('b');
    expect(model.steps[2]!.value.run).toBe('echo c');
  });
});

describe('an unclosed quote in the 4th step', () => {
  const text = `steps:
  - name: s1
    run: echo 1
  - name: s2
    depends: [s1]
    run: echo 2
  - name: s3
    depends: [s2]
    run: echo 3
  - name: s4
    depends: s3
    run: "echo 4
  - name: s5
    depends: [s4]
    run: echo 5
  - name: s6
    depends: [s5]
    run: 'echo 6'
`;

  it('keeps the steps before and after it', () => {
    const model = parseDagDocLenient(text);
    expect(model.ok).toBe(true);
    expect(model.steps).toHaveLength(6);
    expect(model.steps.map((s) => s.explicitName)).toEqual([
      's1',
      's2',
      's3',
      's4',
      's5',
      's6',
    ]);
    expect(model.steps.map((s) => s.broken !== undefined)).toEqual([
      false,
      false,
      false,
      true,
      false,
      false,
    ]);
    expect(model.steps[3]!.depends).toEqual(['s3']);
    expect(model.steps[5]!.value.run).toBe('echo 6');
    expect(edgePairs(model)).toHaveLength(5);
  });
});

describe('a type error in depends', () => {
  const text = `steps:
  - name: a
    run: echo a
  - name: b
    depends: {a: 1}
    run: echo b
  - name: c
    depends: [a, {x: y}]
    run: echo c
  - name: d
    depends: [a]
    run: echo d
`;

  it('flags the step and keeps the others', () => {
    const model = parseDagDocLenient(text);
    expect(model.ok).toBe(true);
    expect(model.steps.map((s) => s.problem !== undefined)).toEqual([
      false,
      true,
      true,
      false,
    ]);
    const graph = graphOf(model);
    expect(graph.nodes[1]!.issues.errors).toBe(1);
    expect(graph.nodes[1]!.issueMessages?.errors[0]).toMatch(/depends/);
    expect(edgePairs(model)).toEqual([
      'steps[0]>steps[2]',
      'steps[0]>steps[3]',
    ]);
  });
});

describe('a step that the server refuses', () => {
  const text = `steps:
  - name: a
    run: echo a
  - name: b
    depends: [a]
    run: ""
  - name: c
    depends: [b]
    action: http.request
`;

  it('stays a normal node with its issue and its edges', () => {
    const model = parseDagDocLenient(text);
    expect(model.ok).toBe(true);
    expect(model.parseErrors).toEqual([]);
    const issues: ValidationIssue[] = [
      {
        message: "field 'steps': field 'command': step command is empty",
        severity: ValidationIssueSeverity.error,
        source: ValidationIssueSource.build,
      },
    ];
    const graph = graphOf(model, issues);
    expect(graph.nodes.map((n) => n.issues.errors)).toEqual([0, 1, 0]);
    expect(graph.nodes[1]!.issueMessages?.errors[0]).toMatch(/empty/);
    expect(edgePairs(model, issues)).toEqual([
      'steps[0]>steps[1]',
      'steps[1]>steps[2]',
    ]);
  });

  it('lets the edits of the other steps work', () => {
    const result = applyEdit(text, {
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['run'],
      value: 'echo changed',
    });
    expect(result.ok && result.text).toBe(
      text.replace('run: echo a', 'run: echo changed')
    );
  });
});

describe('edits next to a broken step', () => {
  const edit = (text: string, op: Parameters<typeof applyEdit>[1]) =>
    applyEdit(text, op);

  it('edits another step and leaves the broken text as it is', () => {
    const result = edit(BROKEN_THIRD, {
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['run'],
      value: 'echo changed',
    });
    expect(result.ok && result.text).toBe(
      BROKEN_THIRD.replace('run: echo a', 'run: echo changed')
    );
    expect(result.ok && result.edits).toHaveLength(1);
  });

  it('edits a step after the broken one', () => {
    const result = edit(BROKEN_THIRD, {
      kind: 'setStepField',
      step: 'steps[4]',
      path: ['run'],
      value: 'echo changed',
    });
    expect(result.ok && result.text).toBe(
      BROKEN_THIRD.replace('run: echo e', 'run: echo changed')
    );
  });

  it('adds a step at the end', () => {
    const result = edit(BROKEN_THIRD, {
      kind: 'addStep',
      step: { run: 'echo f' },
      after: 'steps[4]',
    });
    expect(result.ok && result.text.startsWith(BROKEN_THIRD)).toBe(true);
    expect(result.ok && result.edits).toHaveLength(1);
    if (result.ok) {
      const model = parseDagDocLenient(result.text);
      expect(model.steps).toHaveLength(6);
      expect(model.steps[3]!.value.run).toBe('echo d');
      expect(model.steps[2]!.broken).toBeDefined();
    }
  });

  it('adds a step between two steps around the broken one', () => {
    const result = edit(BROKEN_THIRD, {
      kind: 'addStep',
      step: { run: 'echo x', name: 'x' },
      after: 'steps[3]',
    });
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.text).toContain(
        '    run: echo d\n  - name: x\n    run: echo x\n  - name: e'
      );
      expect(result.text).toContain('    run: [echo, c\n');
    }
  });

  it('refuses an edit of the broken step', () => {
    const result = edit(BROKEN_THIRD, {
      kind: 'setStepField',
      step: 'steps[2]',
      path: ['run'],
      value: 'echo c',
    });
    expect(result.ok).toBe(false);
  });

  it('refuses to delete the broken step but not another one', () => {
    expect(
      edit(BROKEN_THIRD, {
        kind: 'deleteStep',
        step: 'steps[2]',
        rewire: false,
      }).ok
    ).toBe(false);
    const other = edit(BROKEN_THIRD, {
      kind: 'deleteStep',
      step: 'steps[4]',
      rewire: false,
    });
    expect(other.ok && other.text).toBe(
      BROKEN_THIRD.replace(
        '  - name: e\n    depends: [d]\n    run: echo e\n',
        ''
      )
    );
  });

  it('fixes the broken step with its YAML', () => {
    const result = edit(BROKEN_THIRD, {
      kind: 'replaceStepYaml',
      step: 'steps[2]',
      yaml: 'name: c\ndepends: [b]\nrun: echo c\n',
    });
    expect(result.ok && result.text).toBe(
      BROKEN_THIRD.replace('run: [echo, c\n', 'run: echo c\n')
    );
    expect(result.ok && parseDagDoc(result.text).ok).toBe(true);
  });

  it('keeps the other failures of an edit', () => {
    const result = edit(BROKEN_THIRD, {
      kind: 'deleteStep',
      step: 'steps[9]',
      rewire: false,
    });
    expect(result).toMatchObject({ ok: false, code: 'unknownStep' });
  });
});

describe('undo and redo around a break', () => {
  it('reads every text of the history', () => {
    const good = BROKEN_THIRD.replace('run: [echo, c', 'run: echo c');
    const history = createTextHistory(good);
    history.push(BROKEN_THIRD);
    history.push(good);
    const counts = (text: string | null) => {
      const model = parseDagDocLenient(text ?? '');
      return [model.ok, model.steps.length, model.parseErrors.length];
    };
    expect(counts(good)).toEqual([true, 5, 0]);
    expect(counts(history.undo())).toEqual([true, 5, 1]);
    expect(counts(history.undo())).toEqual([true, 5, 0]);
    expect(counts(history.redo())).toEqual([true, 5, 1]);
    expect(counts(history.redo())).toEqual([true, 5, 0]);
  });
});

describe.skipIf(realSchema === null)('the steps of the add step panel', () => {
  const BASE = 'steps:\n  - name: first\n    run: echo hi\n';
  const entries = realSchema ? buildActionCatalog(realSchema, []) : [];

  it('has the catalog of the repository schema', () => {
    expect(entries.length).toBeGreaterThan(50);
  });

  it.each(entries.map((entry) => [entry.name, entry] as const))(
    '%s gives a valid step in one hunk',
    (_name, entry) => {
      const result = applyEdit(BASE, {
        kind: 'addStep',
        step: structuredClone(entry.template),
      });
      expect(result.ok).toBe(true);
      if (!result.ok) return;
      expect(result.edits).toHaveLength(1);
      expect(result.text.startsWith(BASE)).toBe(true);
      const model = parseDagDoc(result.text);
      expect(model.ok).toBe(true);
      for (const step of model.steps) {
        expect(checkStepSchema(realSchema!, step.value)).toEqual([]);
        // An empty required value does not pass the validation of the server.
        expect(JSON.stringify(step.value)).not.toMatch(
          /"(command|run|url|query|message|path|key|filter|script)":""/
        );
      }
    }
  );

  it('adds a target step for a router', () => {
    const entry = entries.find((e) => e.name === 'router.route')!;
    const result = applyEdit(BASE, {
      kind: 'addStep',
      step: structuredClone(entry.template),
    });
    expect(result.ok && result.text).toBe(
      `${BASE}  - name: router_route
    action: router.route
    with:
      value: ok
      routes:
        ok: [ok]
  - name: ok
    depends: [router_route]
    run: echo "TODO"
`
    );
  });

  it('gives an explicit id to a human task', () => {
    const entry = entries.find((e) => e.name === 'human.task')!;
    const add = (text: string) =>
      applyEdit(text, {
        kind: 'addStep',
        step: structuredClone(entry.template),
      });
    const first = add(BASE);
    expect(first.ok && first.text).toContain('id: approval');
    const second = first.ok ? add(first.text) : first;
    expect(second.ok && second.text).toContain('id: approval_2');
  });
});

describe('parse time of a broken 100-step document', () => {
  it('stays fast', () => {
    const steps = Array.from(
      { length: 100 },
      (_, i) =>
        `  - name: step-${i}\n    depends: [${i === 0 ? '' : `step-${i - 1}`}]\n` +
        `    run: ${i === 50 ? '"echo' : `echo ${i}`}\n`
    );
    const text = `name: large\nsteps:\n${steps.join('')}`;
    const start = performance.now();
    const model = parseDagDocLenient(text);
    const ms = performance.now() - start;
    expect(model.ok).toBe(true);
    expect(model.steps).toHaveLength(100);
    expect(model.steps.filter((s) => s.broken !== undefined)).toHaveLength(1);
    expect(ms).toBeLessThan(1000);
  });
});
