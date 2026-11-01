// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from 'vitest';
import { ReferenceScopeStepOutputsKnown } from '@/api/v1/schema';
import type { ReferenceScope } from '../../contracts';
import {
  scopeHttpJq,
  yamlArraySteps,
  yamlHttpJq,
} from '../../testing/fixtures';
import { registerReferenceCompletion } from '../monacoCompletion';
import { computeReferenceHover, registerReferenceHover } from '../monacoHover';

const state = { scope: scopeHttpJq };
const at = (text: string, marker: string, shift = 0) =>
  text.indexOf(marker) + shift;

describe('computeReferenceHover', () => {
  it('gives the label, detail and group of a usable candidate', () => {
    const token = '${steps.fetch.outputs.body}';
    const offset = at(yamlHttpJq, token, 4);
    const hover = computeReferenceHover(yamlHttpJq, offset, state);
    expect(hover).toMatchObject({
      from: at(yamlHttpJq, token),
      to: at(yamlHttpJq, token) + token.length,
    });
    expect(hover?.lines[0]).toBe('fetch.body - string · action');
    expect(hover?.lines).toContain('Step outputs');
    expect(hover?.lines).not.toContain('Known after run');
  });

  it('says that a runtime candidate is known after the run', () => {
    const scope: ReferenceScope = {
      ...scopeHttpJq,
      steps: scopeHttpJq.steps.map((s) =>
        s.name === 'fetch'
          ? { ...s, outputsKnown: ReferenceScopeStepOutputsKnown.runtime }
          : s
      ),
    };
    const offset = at(yamlHttpJq, '${steps.fetch.outputs.body}', 4);
    const hover = computeReferenceHover(yamlHttpJq, offset, { scope });
    expect(hover?.lines).toContain('Known after run');
  });

  it('describes a variable candidate with its group', () => {
    const offset = at(yamlHttpJq, '${params.API_URL}', 4);
    const hover = computeReferenceHover(yamlHttpJq, offset, state);
    expect(hover?.lines[0]).toBe('API_URL - = https://api.example.com/items');
    expect(hover?.lines[1]).toBe('Parameters');
  });

  it('flags a step output that this step cannot use', () => {
    const text = yamlHttpJq.replace(
      '${params.API_URL}',
      '${steps.extract.outputs.result}'
    );
    const offset = at(text, '${steps.extract', 4);
    expect(computeReferenceHover(text, offset, state)?.lines).toEqual([
      'Not available in this step',
    ]);
  });

  it('marks a legacy token', () => {
    const text = yamlHttpJq.replace('${params.API_URL}', '${fetch.stdout}');
    const hover = computeReferenceHover(text, at(text, '${fetch', 3), state);
    expect(hover?.lines).toEqual(['Legacy reference form']);
  });

  it('uses the page language', () => {
    const text = yamlHttpJq.replace('${params.API_URL}', '${fetch.stdout}');
    document.documentElement.lang = 'ja';
    try {
      const hover = computeReferenceHover(text, at(text, '${fetch', 3), state);
      expect(hover?.lines).toEqual(['旧形式の参照']);
    } finally {
      document.documentElement.lang = 'en';
    }
  });

  it('explains shell syntax in a run field only', () => {
    const text = yamlArraySteps.replace('./publish.sh', '${HOME}/publish.sh');
    const offset = at(text, '${HOME}', 3);
    expect(computeReferenceHover(text, offset, state)?.lines).toEqual([
      '${HOME} is shell syntax here. Use ${env.HOME} for a Dagu reference.',
    ]);
    const other = yamlHttpJq.replace('${params.API_URL}', '${HOME}');
    expect(
      computeReferenceHover(other, at(other, '${HOME}', 3), state)
    ).toBeNull();
  });

  it('returns null outside a token', () => {
    expect(
      computeReferenceHover(yamlHttpJq, at(yamlHttpJq, 'GET'), state)
    ).toBeNull();
  });
});

describe('registerReferenceHover', () => {
  function fakeMonaco() {
    const disposeCompletion = vi.fn();
    const disposeHover = vi.fn();
    const registerHoverProvider = vi.fn(() => ({ dispose: disposeHover }));
    const monaco = {
      languages: {
        registerCompletionItemProvider: vi.fn(() => ({
          dispose: disposeCompletion,
        })),
        registerHoverProvider,
        CompletionItemKind: { Variable: 4 },
        CompletionTriggerKind: { Invoke: 0 },
      },
    } as unknown as typeof import('monaco-editor');
    return { monaco, registerHoverProvider, disposeCompletion, disposeHover };
  }

  it('returns markdown lines with the token range', () => {
    const { monaco, registerHoverProvider } = fakeMonaco();
    registerReferenceHover(monaco, () => state);
    const [language, provider] = registerHoverProvider.mock
      .calls[0] as unknown as [
      string,
      {
        provideHover(
          model: unknown,
          position: { lineNumber: number; column: number }
        ): {
          range: Record<string, number>;
          contents: { value: string }[];
        } | null;
      },
    ];
    expect(language).toBe('yaml');
    const token = '${steps.fetch.outputs.body}';
    const offset = at(yamlHttpJq, token, 4);
    const lines = yamlHttpJq.split('\n');
    const lineIndex = yamlHttpJq.slice(0, offset).split('\n').length - 1;
    const lineStart = lines
      .slice(0, lineIndex)
      .reduce((n, l) => n + l.length + 1, 0);
    const model = {
      getValue: () => yamlHttpJq,
      getOffsetAt: () => offset,
      getPositionAt: (o: number) => ({
        lineNumber: lineIndex + 1,
        column: o - lineStart + 1,
      }),
    };
    const hover = provider.provideHover(model, {
      lineNumber: lineIndex + 1,
      column: 1,
    });
    expect(hover?.contents[0]?.value).toBe('fetch.body - string · action');
    expect(hover?.range).toMatchObject({
      startLineNumber: lineIndex + 1,
      endLineNumber: lineIndex + 1,
      endColumn: lines[lineIndex]!.length + 1,
    });
    const outside = provider.provideHover(
      { ...model, getOffsetAt: () => 0 },
      {
        lineNumber: 1,
        column: 1,
      }
    );
    expect(outside).toBeNull();
  });

  it('is disposed together with the completion provider', () => {
    const { monaco, disposeCompletion, disposeHover } = fakeMonaco();
    const handle = registerReferenceCompletion(monaco, () => state);
    handle.dispose();
    expect(disposeCompletion).toHaveBeenCalledTimes(1);
    expect(disposeHover).toHaveBeenCalledTimes(1);
  });
});
