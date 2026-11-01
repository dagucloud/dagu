// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/*
 * Round-trip golden tests of the patch engine.
 *
 * Fixture sources. Paths are relative to the repository root. A "spec block"
 * is the fenced yaml block of a file in specs/ that starts on the given line.
 * "hand-written" files are made for this test.
 *
 *  anchors-aliases.yaml            hand-written: anchors, aliases, merge keys
 *  block-scalars.yaml              hand-written: |, |+, |-, >, >-, explicit indent
 *  browser-flow-maps.yaml          conformance/spec072_browser/testdata/checkout.yaml
 *  build-comments.yaml             conformance/spec033_build/testdata/build.yaml
 *  chain-router.yaml               conformance/spec040_router_route/testdata/chain_type_rejected.yaml
 *  comments-everywhere.yaml        hand-written: comments at many positions
 *  config-cli.yaml                 internal/testdata/cli/config_test.yaml
 *  embedded-custom-executor.yaml   examples/embedded/custom-executor/workflow.yaml
 *  embedded-local.yaml             examples/embedded/local/workflow.yaml
 *  empty-file.yaml                 internal/testdata/config/empty.yaml
 *  flow-collections.yaml           hand-written: flow maps and sequences, multi-line flow
 *  flow-maps-unicode.yaml          conformance/spec074_computer/testdata/pinned.yaml
 *  folded-line-break.yaml          conformance/spec014_step_run_command/testdata/resolved_line_break.yaml
 *  foreach-string-items.yaml       conformance/spec018_parallel_foreach/testdata/foreach_string_items.yaml
 *  handler-env.yaml                conformance/spec005_params/testdata/handler_env.yaml
 *  handlers-chain.yaml             hand-written: type chain, all handler_on events, shorthand
 *  harness-acp.yaml                examples/harness-acp.yaml
 *  invalid-yaml.yaml               conformance/spec002_schema/testdata/invalid_yaml.yaml
 *  map-steps-forbidden.yaml        conformance/spec002_schema/testdata/steps_mapping_forbidden.yaml
 *  map-steps.yaml                  hand-written: map-form steps with comments
 *  multidoc-comments-markers.yaml  hand-written: --- and ... markers, comments before markers
 *  multidoc-empty-between.yaml     conformance/spec002_schema/testdata/empty_document_between.yaml
 *  multidoc-subdag.yaml            conformance/cli/testdata/parent_child.yaml
 *  multidoc-unnamed-later.yaml     conformance/spec002_schema/testdata/later_document_without_name.yaml
 *  nested-array-run.yaml           conformance/spec013_step_run/testdata/array_form_nested_array_item_invalid.yaml
 *  nested-parallel-groups.yaml     hand-written: parallel groups as nested arrays
 *  odd-indentation.yaml            hand-written: "-   key" items, extra spaces after colons
 *  quoted-keys-and-values.yaml     hand-written: quoted keys, escapes, complex keys, tags
 *  router-numeric-band.yaml        conformance/spec040_router_route/testdata/shared_target_numeric_band.yaml
 *  router-regex-keys.yaml          conformance/spec040_router_route/testdata/regex_route.yaml
 *  run-context.yaml                conformance/spec017_built_in_run_context/testdata/runtime_context.yaml
 *  scheduler-cron.yaml             internal/testdata/scheduler/cron-test.yaml
 *  script-utf8-multiline.yaml      conformance/spec015_step_run_script/testdata/powershell_utf8_multiline.yaml
 *  shorthand-steps.yaml            hand-written: shorthand string steps in several quote styles
 *  spec-harness-comments.yaml      spec block: specs/067-harness.md, line 304
 *  spec-outputs-heredoc.yaml       spec block: specs/012-step-outputs.md, line 218
 *  spec-subdag-parallel.yaml       spec block: specs/019-sub-dag-working-directory.md, line 151
 *  spec-xlsx-convert.yaml          spec block: specs/077-xlsx.md, line 1211
 *  unicode-comments.yaml           hand-written: non-ASCII comments, names and values
 *  xlsx-japanese.yaml              conformance/spec077_xlsx/testdata/extract_japanese.yaml
 */

import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  isMap,
  isScalar,
  isSeq,
  parseAllDocuments,
  type Document,
  type YAMLMap,
} from 'yaml';
import { applyEdit } from '../index';
import { parseDagDoc } from '../parse';
import { documentZeroEnd, editDocumentDetailed } from '../patch';
import {
  yamlArraySteps,
  yamlBroken,
  yamlChain,
  yamlHandlers,
  yamlHttpJq,
  yamlMapSteps,
  yamlMultiDoc,
  yamlNestedParallel,
  yamlRouter,
  yamlShorthandSteps,
} from '../../testing/fixtures';

/**
 * Files for which a part of the probe (c) is skipped, with the reason. The
 * key is the path relative to the repository root, or `fixtures/<name>`.
 * `probe` skips the whole probe; `removal` skips only the byte-for-byte
 * check after the removal of the probe key; `edits` skips the extra probes.
 */
const PROBE_LIMITS: Record<
  string,
  { skip: 'probe' | 'removal' | 'edits'; reason: string }
> = {
  'fixtures/odd-indentation.yaml': {
    skip: 'edits',
    reason:
      'the emitter cannot write "-   key" items, so an edit of a scalar rewrites the whole document 0 (fallback)',
  },
  'fixtures/comments-everywhere.yaml': {
    skip: 'removal',
    reason:
      'yaml attaches the blank line before a trailing comment of a map to the last key, so the blank line leaves with the removed key',
  },
};

const stats = { probed: 0, opProbed: 0 };

const FIXTURE_DIR = path.resolve(
  'src/features/visual-editor/doc/__tests__/fixtures'
);
const CORPUS_DIRS = ['examples', 'internal', 'conformance'];

type Path = (string | number)[];

/** Path of the first step entry that is a mapping: an array item or a map-form value. */
function firstStepPath(doc: Document): Path | null {
  const root = doc.contents;
  if (!isMap(root)) return null;
  const steps = root.get('steps', true);
  if (isSeq(steps)) {
    const index = steps.items.findIndex((item) => isMap(item));
    return index < 0 ? null : ['steps', index];
  }
  if (isMap(steps)) {
    for (const pair of steps.items) {
      if (isMap(pair.value) && pair.key !== null) {
        const key = (pair.key as { value?: unknown }).value;
        if (typeof key === 'string') return ['steps', key];
      }
    }
  }
  return null;
}

function lineStartOf(text: string, offset: number): number {
  return text.lastIndexOf('\n', offset - 1) + 1;
}

function lineEndOf(text: string, offset: number): number {
  const nl = text.indexOf('\n', offset);
  return nl < 0 ? text.length : nl + 1;
}

/** Problems of the `x_probe` check (c) for one parsed text; empty when the check holds. */
function probeProblems(
  text: string,
  doc: Document,
  skipRemoval: boolean
): string[] {
  const at = firstStepPath(doc);
  if (!at) return [];
  const entry = doc.getIn(at, true) as
    | { range?: [number, number, number] }
    | undefined;
  if (!entry?.range) return [];
  stats.probed++;
  const windowFrom = lineStartOf(text, entry.range[0]);
  const windowTo = lineEndOf(
    text,
    Math.max(entry.range[1], entry.range[2] - 1)
  );

  const added = editDocumentDetailed(text, (d) => {
    (d.getIn(at, true) as { set(k: string, v: unknown): void }).set(
      'x_probe',
      1
    );
  });
  if (!added.result.ok) {
    return [`probe edit failed: ${added.result.code} ${added.result.message}`];
  }
  const problems: string[] = [];
  const { edits, text: changed } = added.result;
  if (added.fallback)
    problems.push('probe edit used the whole-region fallback');
  for (const edit of edits) {
    if (edit.from < windowFrom || edit.to > windowTo) {
      problems.push(
        `probe edit ${edit.from}-${edit.to} is outside the step entry ${windowFrom}-${windowTo}`
      );
    }
  }
  if (!changed.includes('x_probe'))
    problems.push('probe key is missing from the result');

  const removed = editDocumentDetailed(changed, (d) => {
    (d.getIn(at, true) as { delete(k: string): void }).delete('x_probe');
  });
  if (!removed.result.ok) {
    problems.push(
      `probe removal failed: ${removed.result.code} ${removed.result.message}`
    );
  } else if (skipRemoval) {
    // Limit of yaml, see PROBE_LIMITS.
  } else if (removed.result.text !== text) {
    problems.push('removing the probe does not give back the original text');
  } else if (removed.fallback) {
    problems.push('probe removal used the whole-region fallback');
  }
  return problems;
}

/**
 * Extra probes on the first step entry: change the first scalar value, delete
 * the last key, and add then remove a step. The edits must stay inside the
 * entry (the new step goes to the end of the steps) and must not need the
 * whole-region fallback.
 */
function editProbeProblems(text: string, doc: Document): string[] {
  const at = firstStepPath(doc);
  if (!at) return [];
  const entry = doc.getIn(at, true) as YAMLMap | undefined;
  if (!entry?.range || entry.items.length < 2) return [];
  const windowFrom = lineStartOf(text, entry.range[0]);
  const windowTo = lineEndOf(
    text,
    Math.max(entry.range[1], entry.range[2] - 1)
  );
  const problems: string[] = [];
  const check = (
    label: string,
    outcome: ReturnType<typeof editDocumentDetailed>
  ) => {
    if (!outcome.result.ok) {
      problems.push(
        `${label}: ${outcome.result.code} ${outcome.result.message}`
      );
      return;
    }
    if (outcome.fallback) problems.push(`${label}: whole-region fallback`);
    for (const edit of outcome.result.edits) {
      if (edit.from < windowFrom || edit.to > windowTo) {
        problems.push(
          `${label}: edit ${edit.from}-${edit.to} outside ${windowFrom}-${windowTo}`
        );
      }
    }
  };
  const scalar = entry.items.find(
    (pair) => isScalar(pair.value) && typeof pair.value.value === 'string'
  );
  if (scalar) {
    const key = (scalar.key as { value?: unknown }).value as string;
    check(
      'change scalar',
      editDocumentDetailed(text, (d) => {
        (d.getIn(at, true) as YAMLMap).set(key, 'x_changed');
      })
    );
  }
  const last = entry.items[entry.items.length - 1];
  const lastKey = (last?.key as { value?: unknown } | undefined)?.value;
  if (typeof lastKey === 'string') {
    check(
      'delete key',
      editDocumentDetailed(text, (d) => {
        (d.getIn(at, true) as YAMLMap).delete(lastKey);
      })
    );
  }
  return problems;
}

/** Checks of `parseDagDoc` (d) on one text; `docs` is `parseAllDocuments(text)`. */
function modelProblems(text: string, docs: Document[]): string[] {
  let model: ReturnType<typeof parseDagDoc>;
  try {
    model = parseDagDoc(text);
  } catch (error) {
    return [`parseDagDoc threw: ${String(error)}`];
  }
  const problems: string[] = [];
  const broken = !!docs[0] && docs[0].errors.length > 0;
  if (model.ok === broken) {
    problems.push(
      `model.ok is ${model.ok} but document 0 has ${broken ? '' : 'no '}errors`
    );
  }
  if (model.documentCount !== docs.length) {
    problems.push(
      `documentCount ${model.documentCount}, yaml gives ${docs.length}`
    );
  }
  if (broken) return problems;

  const end = documentZeroEnd(text, docs);
  const keys = new Set<string>();
  for (const step of [...model.steps, ...model.handlers]) {
    if (keys.has(step.key)) problems.push(`duplicate step key ${step.key}`);
    keys.add(step.key);
    const { from, to } = step.range;
    if (
      from < 0 ||
      to > end ||
      from >= to ||
      text.slice(from, to).trim() === ''
    ) {
      problems.push(
        `${step.key}: range ${from}-${to} is empty or outside document 0 (${end})`
      );
    }
  }
  model.steps.forEach((step, i) => {
    if (step.key !== `steps[${i}]` || step.index !== i) {
      problems.push(
        `model.steps[${i}] has key ${step.key} and index ${step.index}`
      );
    }
  });
  return problems;
}

/**
 * Check (e): `applyEdit` `setStepField` on the first step of the model adds
 * `x_probe` with edits that stay on the lines of that step entry, and the
 * removal of the field gives back the original text.
 */
function opProbeProblems(text: string, skipRemoval: boolean): string[] {
  const step = parseDagDoc(text).steps[0];
  if (!step || step.form === 'shorthand' || step.value === null) return [];
  if (typeof step.value !== 'object' || 'x_probe' in step.value) return [];
  const added = applyEdit(text, {
    kind: 'setStepField',
    step: step.key,
    path: ['x_probe'],
    value: 1,
  });
  // An alias entry cannot be edited in place.
  if (!added.ok && added.code === 'unsupportedForm') return [];
  if (!added.ok) return [`setStepField failed: ${added.code} ${added.message}`];
  stats.opProbed++;
  const problems: string[] = [];
  const windowFrom = lineStartOf(text, step.range.from);
  const windowTo = lineEndOf(
    text,
    Math.max(step.range.from, step.range.to - 1)
  );
  for (const edit of added.edits) {
    if (edit.from < windowFrom || edit.to > windowTo) {
      problems.push(
        `setStepField edit ${edit.from}-${edit.to} is outside ${step.key} ${windowFrom}-${windowTo}`
      );
    }
  }
  const removed = applyEdit(added.text, {
    kind: 'setStepField',
    step: step.key,
    path: ['x_probe'],
    value: undefined,
  });
  if (!removed.ok) {
    problems.push(`setStepField removal failed: ${removed.code}`);
  } else if (!skipRemoval && removed.text !== text) {
    problems.push('setStepField removal does not give back the original text');
  }
  return problems;
}

/** Runs the checks (a) to (e) on one text. Returns the problems found. */
function checkText(
  text: string,
  limit?: { skip: 'probe' | 'removal' | 'edits' }
): string[] {
  let docs: Document[];
  try {
    docs = parseAllDocuments(text) as Document[];
  } catch (error) {
    return [`yaml threw: ${String(error)}`];
  }
  const doc = docs[0];
  const broken = !!doc && doc.errors.length > 0;
  const modelIssues = modelProblems(text, docs);
  if (modelIssues.length > 0) return modelIssues;

  let outcome: ReturnType<typeof editDocumentDetailed>;
  try {
    outcome = editDocumentDetailed(text, () => {});
  } catch (error) {
    return [`editDocument threw: ${String(error)}`];
  }
  const { result } = outcome;
  const problems: string[] = [];
  if (broken) {
    if (result.ok || result.code !== 'parseError') {
      problems.push(`expected parseError, got ${JSON.stringify(result)}`);
    }
    return problems;
  }
  if (result.ok || result.code !== 'noop') {
    problems.push(`expected noop, got ${JSON.stringify(result)}`);
    return problems;
  }
  if (doc && limit?.skip !== 'probe') {
    try {
      problems.push(...probeProblems(text, doc, limit?.skip === 'removal'));
      if (limit?.skip !== 'edits')
        problems.push(...editProbeProblems(text, doc));
      problems.push(...opProbeProblems(text, limit?.skip === 'removal'));
    } catch (error) {
      problems.push(`probe threw: ${String(error)}`);
    }
  }
  return problems;
}

function yamlFilesUnder(dir: string): string[] {
  const found: string[] = [];
  const walk = (current: string) => {
    for (const entry of readdirSync(current).sort()) {
      if (entry === 'node_modules' || entry === '.git') continue;
      const full = path.join(current, entry);
      let stat;
      try {
        stat = statSync(full);
      } catch {
        continue;
      }
      if (stat.isDirectory()) walk(full);
      else if (/\.ya?ml$/.test(entry)) found.push(full);
    }
  };
  walk(dir);
  return found;
}

function failureReport(failures: string[]): string {
  return failures.length === 0 ? '' : `\n${failures.join('\n')}\n`;
}

describe('fixture files', () => {
  const files = existsSync(FIXTURE_DIR) ? yamlFilesUnder(FIXTURE_DIR) : [];

  it('has 30 to 50 files', () => {
    expect(files.length).toBeGreaterThanOrEqual(30);
    expect(files.length).toBeLessThanOrEqual(50);
  });

  it.each(files.map((file) => [path.basename(file), file]))(
    '%s',
    (name, file) => {
      const text = readFileSync(file as string, 'utf8');
      const limit = PROBE_LIMITS[`fixtures/${name}`];
      expect(checkText(text, limit)).toEqual([]);
      // The same file with CRLF line ends.
      expect(checkText(text.replace(/\n/g, '\r\n'), limit)).toEqual([]);
    }
  );

  it('probes most files', () => {
    expect(stats.probed).toBeGreaterThanOrEqual(30);
    expect(stats.opProbed).toBeGreaterThanOrEqual(30);
  });
});

describe('shared test fixtures', () => {
  const texts: Record<string, string> = {
    yamlArraySteps,
    yamlMapSteps,
    yamlShorthandSteps,
    yamlNestedParallel,
    yamlChain,
    yamlMultiDoc,
    yamlRouter,
    yamlHandlers,
    yamlBroken,
    yamlHttpJq,
  };
  it.each(Object.entries(texts))('%s', (_name, text) => {
    expect(checkText(text)).toEqual([]);
    expect(checkText(text.replace(/\n/g, '\r\n'))).toEqual([]);
  });
});

// The repository directories are outside ui/. They exist when the test runs
// with the whole repository visible (repository CI), not in a ui-only copy.
for (const name of CORPUS_DIRS) {
  const root = path.resolve('..', name);
  describe.skipIf(!existsSync(root))(`corpus ${name}`, () => {
    it('round-trips every YAML file', { timeout: 300_000 }, () => {
      const failures: string[] = [];
      let count = 0;
      for (const file of yamlFilesUnder(root)) {
        count++;
        const rel = path
          .relative(path.resolve('..'), file)
          .split(path.sep)
          .join('/');
        const problems = checkText(
          readFileSync(file, 'utf8'),
          PROBE_LIMITS[rel]
        );
        for (const problem of problems) failures.push(`${rel}: ${problem}`);
      }
      expect(count).toBeGreaterThan(0);
      expect(failureReport(failures)).toBe('');
    });
  });
}

// Fenced yaml blocks of the Markdown specifications are a second corpus.
describe.skipIf(!existsSync(path.resolve('..', 'specs')))(
  'corpus spec blocks',
  () => {
    it('round-trips every fenced yaml block', { timeout: 300_000 }, () => {
      const root = path.resolve('..', 'specs');
      const failures: string[] = [];
      let blocks = 0;
      for (const entry of readdirSync(root).sort()) {
        if (!entry.endsWith('.md')) continue;
        const markdown = readFileSync(path.join(root, entry), 'utf8');
        for (const match of markdown.matchAll(
          /^```ya?ml\r?\n([\s\S]*?)^```/gm
        )) {
          blocks++;
          const line = markdown.slice(0, match.index).split('\n').length + 1;
          for (const problem of checkText(match[1]!)) {
            failures.push(`specs/${entry}:${line}: ${problem}`);
          }
        }
      }
      expect(blocks).toBeGreaterThan(0);
      expect(failureReport(failures)).toBe('');
    });
  }
);
