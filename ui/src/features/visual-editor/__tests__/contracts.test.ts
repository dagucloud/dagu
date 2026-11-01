// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, expectTypeOf, it } from 'vitest';
import { isMap, isSeq, parseAllDocuments, parseDocument } from 'yaml';
import { NodeStatus } from '@/api/v1/schema';
import {
  catalogSample,
  modelArraySteps,
  overlaySample,
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
} from '../testing/fixtures';
import type {
  CanvasCommand,
  EditorHostProps,
  EditResult,
  PaletteTarget,
  StepKey,
  VisualCanvasProps,
  VisualEditorHandle,
} from '../contracts';
import type { CanvasActions } from '../canvas/context';
import type { VisualEditorProps } from '../VisualEditor';
import type { DagDoc } from '../doc/ops/clipboard';
import {
  copyStepsYaml,
  duplicateStepsOp,
  pasteStepsOp,
} from '../doc/ops/clipboard';

const validYaml: Record<string, string> = {
  yamlArraySteps,
  yamlMapSteps,
  yamlShorthandSteps,
  yamlNestedParallel,
  yamlChain,
  yamlMultiDoc,
  yamlRouter,
  yamlHandlers,
  yamlHttpJq,
};

/** 1-based line and column of a UTF-16 offset. */
function position(text: string, offset: number) {
  const before = text.slice(0, offset);
  const line = before.split('\n').length;
  return { line, column: offset - (before.lastIndexOf('\n') + 1) + 1 };
}

describe('visual editor fixtures', () => {
  it.each(Object.entries(validYaml))('%s parses without errors', (_, text) => {
    const docs = parseAllDocuments(text);
    expect(docs.length).toBeGreaterThan(0);
    for (const doc of docs) {
      expect(doc.errors).toEqual([]);
    }
  });

  it('yamlBroken has a syntax error', () => {
    const docs = parseAllDocuments(yamlBroken);
    expect(docs.some((doc) => doc.errors.length > 0)).toBe(true);
  });

  it('yamlMultiDoc has two documents', () => {
    expect(parseAllDocuments(yamlMultiDoc)).toHaveLength(2);
  });

  it('modelArraySteps holds the text of yamlArraySteps', () => {
    expect(modelArraySteps.text).toBe(yamlArraySteps);
    expect(modelArraySteps.steps).toHaveLength(3);
  });

  it('step ranges match the text, lines and columns', () => {
    const doc = parseDocument(yamlArraySteps);
    const steps = doc.get('steps', true);
    expect(isSeq(steps)).toBe(true);
    if (!isSeq(steps)) return;

    modelArraySteps.steps.forEach((step, i) => {
      const { range } = step;
      const slice = yamlArraySteps.slice(range.from, range.to);
      expect(slice.startsWith(`name: ${step.explicitName}`)).toBe(true);
      expect(slice.endsWith('\n')).toBe(false);
      expect(yamlArraySteps.slice(range.from - 2, range.from)).toBe('- ');

      // The range starts where the parser puts the entry.
      const item = steps.items[i];
      expect(isMap(item)).toBe(true);
      expect(isMap(item) ? item.range?.[0] : undefined).toBe(range.from);

      expect(position(yamlArraySteps, range.from)).toEqual({
        line: range.startLine,
        column: range.startColumn,
      });
      expect(position(yamlArraySteps, range.to)).toEqual({
        line: range.endLine,
        column: range.endColumn,
      });
      expect(step.key).toBe(`steps[${i}]`);
      expect(step.index).toBe(i);
    });
  });

  it('step values equal the parsed entries', () => {
    const plain = parseDocument(yamlArraySteps).toJS() as {
      steps: Record<string, unknown>[];
    };
    modelArraySteps.steps.forEach((step, i) => {
      expect(step.value).toEqual(plain.steps[i]);
    });
  });

  it('catalogSample names are unique', () => {
    const names = catalogSample.map((entry) => entry.name);
    expect(new Set(names).size).toBe(names.length);
  });

  it('overlaySample holds success, failed and running', () => {
    const statuses = Object.values(overlaySample).map((s) => s.status);
    expect(statuses).toEqual(
      expect.arrayContaining([
        NodeStatus.Success,
        NodeStatus.Failed,
        NodeStatus.Running,
      ])
    );
  });
});

// parity:test-visual-editor
describe('host seam contracts', () => {
  it('adds the host command and target kinds', () => {
    expectTypeOf<Extract<CanvasCommand, { kind: 'step' }>>().toHaveProperty(
      'cmd'
    );
    expectTypeOf<CanvasCommand['kind']>().toEqualTypeOf<
      | 'openInspector'
      | 'runStep'
      | 'showRun'
      | 'rename'
      | 'showYaml'
      | 'copy'
      | 'paste'
      | 'duplicate'
      | 'step'
    >();
    expectTypeOf<PaletteTarget['mode']>().toEqualTypeOf<
      'after' | 'child' | 'insert' | 'route' | 'parallel' | 'first'
    >();
    expectTypeOf<Extract<PaletteTarget, { mode: 'route' }>>().toEqualTypeOf<{
      mode: 'route';
      router: StepKey;
      port: string;
    }>();
  });

  it('lets the editor and the canvas take the host props', () => {
    expectTypeOf<VisualEditorProps>().toMatchTypeOf<EditorHostProps>();
    expectTypeOf<VisualCanvasProps>().toHaveProperty('viewRef');
    expectTypeOf<VisualCanvasProps>().toHaveProperty('hideFrozenNotice');
    expectTypeOf<CanvasActions>().toHaveProperty('stepCommandGates');
    expectTypeOf<CanvasActions>().toHaveProperty('onRename');
    expectTypeOf<
      VisualEditorHandle['paste']
    >().returns.toEqualTypeOf<EditResult>();
  });

  it('gives the clipboard operations with their signatures', () => {
    expectTypeOf(copyStepsYaml).parameters.toEqualTypeOf<[DagDoc, StepKey[]]>();
    expectTypeOf(pasteStepsOp).parameters.toEqualTypeOf<
      [DagDoc, string, StepKey | null]
    >();
    expect(copyStepsYaml(modelArraySteps, ['steps[0]'])).toContain(
      'name: fetch'
    );
    expect(
      pasteStepsOp(modelArraySteps, '- name: x\n  run: x\n', null)
    ).toMatchObject({ ok: true });
    expect(pasteStepsOp(modelArraySteps, 'not a step', null)).toMatchObject({
      ok: false,
      code: 'invalidYaml',
    });
    expect(duplicateStepsOp(modelArraySteps, ['steps[0]'])).toMatchObject({
      ok: true,
    });
  });
});
