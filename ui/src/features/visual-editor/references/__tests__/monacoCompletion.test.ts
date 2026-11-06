// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  modelArraySteps,
  scopeHttpJq,
  yamlArraySteps,
  yamlHandlers,
  yamlHttpJq,
  yamlMapSteps,
} from '../../testing/fixtures';
import type { ReferenceCandidate, ReferenceScope } from '../../contracts';
import { parseDagDoc } from '../../doc/parse';
import { editorLocale, findOwningStep } from '../editorState';
import {
  computeReferenceCompletions,
  registerFieldReferenceCompletion,
  registerReferenceCompletion,
} from '../monacoCompletion';

const at = (text: string, marker: string, shift = 0) =>
  text.indexOf(marker) + shift;

function scopeWith(
  ...steps: { name: string; stepPath: string }[]
): ReferenceScope {
  const base = scopeHttpJq.steps[0]!;
  return {
    ...scopeHttpJq,
    steps: steps.map((s) => ({
      ...base,
      ...s,
      id: undefined,
      upstream: [],
      outputs: [],
    })),
  };
}

describe('findOwningStep', () => {
  it('uses the model ranges when the model matches the text', () => {
    const state = { scope: scopeHttpJq, model: modelArraySteps };
    expect(
      findOwningStep(yamlArraySteps, at(yamlArraySteps, 'sort data', 2), state)
        ?.name
    ).toBe('extract');
    expect(
      findOwningStep(yamlArraySteps, at(yamlArraySteps, 'curl', 2), state)?.name
    ).toBe('fetch');
  });

  it('returns null for a DAG-level offset and for a step outside the scope', () => {
    const state = { scope: scopeHttpJq, model: modelArraySteps };
    expect(
      findOwningStep(yamlArraySteps, at(yamlArraySteps, 'nightly', 2), state)
    ).toBeNull();
    expect(
      findOwningStep(yamlArraySteps, at(yamlArraySteps, 'publish.sh', 2), state)
    ).toBeNull();
  });

  it('falls back to the YAML path when there is no model', () => {
    const state = { scope: scopeHttpJq };
    expect(
      findOwningStep(yamlArraySteps, at(yamlArraySteps, 'sort data', 2), state)
        ?.name
    ).toBe('extract');
    expect(
      findOwningStep(yamlArraySteps, at(yamlArraySteps, 'nightly', 2), state)
    ).toBeNull();
  });

  it('falls back to the YAML path when the model text is stale', () => {
    const state = { scope: scopeHttpJq, model: modelArraySteps };
    const text = `# edit\n${yamlArraySteps}`;
    expect(findOwningStep(text, at(text, 'curl', 2), state)?.name).toBe(
      'fetch'
    );
  });

  it('finds a step by name in map form', () => {
    const scope = scopeWith(
      { name: 'fetch', stepPath: 'steps[0]' },
      { name: 'parse', stepPath: 'steps[1]' }
    );
    expect(
      findOwningStep(yamlMapSteps, at(yamlMapSteps, 'wc -l', 2), { scope })
        ?.name
    ).toBe('parse');
  });

  it('finds a handler step', () => {
    const scope = scopeWith(
      { name: 'main', stepPath: 'steps[0]' },
      { name: 'onFailure', stepPath: 'handler_on.failure' }
    );
    expect(
      findOwningStep(yamlHandlers, at(yamlHandlers, 'notify', 2), { scope })
        ?.name
    ).toBe('onFailure');
    expect(
      findOwningStep(yamlHandlers, at(yamlHandlers, './job', 2), { scope })
        ?.name
    ).toBe('main');
  });

  it('returns null when there is no scope', () => {
    expect(
      findOwningStep(yamlHttpJq, at(yamlHttpJq, 'filter', 2), {})
    ).toBeNull();
  });
});

describe('computeReferenceCompletions', () => {
  const state = { scope: scopeHttpJq };
  const filterOffset = at(yamlHttpJq, "'[.[]", 3);

  it('lists step outputs first for an explicit request in a value', () => {
    const result = computeReferenceCompletions(
      yamlHttpJq,
      filterOffset,
      state,
      {
        explicit: true,
      }
    );
    expect(result?.items[0]?.token).toBe('${steps.fetch.outputs.body}');
    expect(result?.from).toBe(filterOffset);
    expect(result?.to).toBe(filterOffset);
  });

  it('replaces from the dollar sign after the user types "${"', () => {
    const text = `${yamlHttpJq.slice(0, filterOffset)}\${${yamlHttpJq.slice(filterOffset)}`;
    const offset = filterOffset + 2;
    const result = computeReferenceCompletions(text, offset, state, {
      explicit: false,
    });
    expect(result?.items[0]?.token).toBe('${steps.fetch.outputs.body}');
    expect(result?.from).toBe(filterOffset);
    expect(result?.to).toBe(offset);
  });

  it('replaces the closing brace that the editor added', () => {
    const text = `${yamlHttpJq.slice(0, filterOffset)}\${}${yamlHttpJq.slice(filterOffset)}`;
    const offset = filterOffset + 2;
    const result = computeReferenceCompletions(text, offset, state, {
      explicit: false,
    });
    expect(result?.from).toBe(filterOffset);
    expect(result?.to).toBe(offset + 1);
  });

  it('leaves a lone dollar alone unless the request is explicit', () => {
    const text = `${yamlHttpJq.slice(0, filterOffset)}$${yamlHttpJq.slice(filterOffset)}`;
    const offset = filterOffset + 1;
    expect(
      computeReferenceCompletions(text, offset, state, { explicit: false })
    ).toBeNull();
    const result = computeReferenceCompletions(text, offset, state, {
      explicit: true,
    });
    expect(result?.from).toBe(filterOffset);
    expect(result?.to).toBe(offset);
  });

  it('offers nothing in a param default, which is literal', () => {
    const offset = at(yamlHttpJq, 'https://api', 4);
    expect(
      computeReferenceCompletions(yamlHttpJq, offset, state, { explicit: true })
    ).toBeNull();
  });

  it('gives no step outputs in a param eval', () => {
    const text = 'params:\n  - name: day\n    eval: date\nsteps:\n  - run: a\n';
    const result = computeReferenceCompletions(
      text,
      at(text, 'date', 2),
      state,
      { explicit: true }
    );
    expect(result).not.toBeNull();
    expect(result?.items.some((item) => item.group === 'steps')).toBe(false);
    expect(result?.items.some((item) => item.group === 'params')).toBe(true);
  });

  // Spec 004: a const resolves only the const entries before it, and no
  // runtime namespace.
  it('offers only the earlier consts in a const value', () => {
    const text =
      'consts:\n  - first: 1\n  - second: ${\n  - third: 3\nsteps:\n  - run: a\n';
    const scope: ReferenceScope = {
      ...scopeHttpJq,
      consts: ['first', 'second', 'third'].map((name) => ({
        name,
        reference: `\${consts.${name}}`,
      })),
    };
    const result = computeReferenceCompletions(
      text,
      at(text, '${', 2),
      { scope },
      { explicit: false }
    );
    expect(result?.items.map((item) => item.token)).toEqual([
      '${consts.first}',
    ]);
  });

  it('offers the repeat index for a step with repeat_policy only', () => {
    const text = [
      'steps:',
      '  - name: poll',
      '    run: echo ${',
      '    repeat_policy:',
      '      repeat: while',
      '  - name: once',
      '    run: echo ${',
      '',
    ].join('\n');
    const repeatState = {
      scope: scopeWith(
        { name: 'poll', stepPath: 'steps[0]' },
        { name: 'once', stepPath: 'steps[1]' }
      ),
      model: parseDagDoc(text),
    };
    const labelsAt = (offset: number) =>
      computeReferenceCompletions(text, offset, repeatState, {
        explicit: false,
      })?.items.map((item) => item.label);
    expect(labelsAt(at(text, '${', 2))).toContain('step.repeat_index');
    expect(labelsAt(text.lastIndexOf('${') + 2)).not.toContain(
      'step.repeat_index'
    );
  });

  it('marks a run field and no other field', () => {
    const run = at(yamlArraySteps, 'sort data', 2);
    const inRun = computeReferenceCompletions(
      yamlArraySteps,
      run,
      { scope: scopeHttpJq },
      {
        explicit: true,
      }
    );
    expect(inRun?.inRunField).toBe(true);
    const name = at(yamlArraySteps, 'transform   #', 2);
    const inName = computeReferenceCompletions(
      yamlArraySteps,
      name,
      { scope: scopeHttpJq },
      {
        explicit: true,
      }
    );
    expect(inName?.inRunField).toBe(false);
    const filter = computeReferenceCompletions(
      yamlHttpJq,
      filterOffset,
      state,
      {
        explicit: true,
      }
    );
    expect(filter?.inRunField).toBe(false);
  });

  it('returns null at a key position', () => {
    const offset = at(yamlHttpJq, 'filter:', 2);
    expect(
      computeReferenceCompletions(yamlHttpJq, offset, state, { explicit: true })
    ).toBeNull();
  });

  it('returns null for an implicit request outside an open reference', () => {
    expect(
      computeReferenceCompletions(yamlHttpJq, filterOffset, state, {
        explicit: false,
      })
    ).toBeNull();
  });
});

const KIND_VARIABLE = 4;

function fakeModel(text: string) {
  const lines = text.split('\n');
  return {
    getValue: () => text,
    // Monaco's default word definition, where "$" is a word character.
    getWordUntilPosition: (p: { lineNumber: number; column: number }) => ({
      word:
        /[^\s`~!@#%^&*()\-=+[{\]}\\|;:'",.<>/?]+$/.exec(
          lines[p.lineNumber - 1]!.slice(0, p.column - 1)
        )?.[0] ?? '',
    }),
    getOffsetAt: (p: { lineNumber: number; column: number }) =>
      lines.slice(0, p.lineNumber - 1).reduce((n, l) => n + l.length + 1, 0) +
      p.column -
      1,
    getPositionAt: (offset: number) => {
      let rest = offset;
      for (let i = 0; i < lines.length; i++) {
        const len = lines[i]!.length + 1;
        if (rest < len) return { lineNumber: i + 1, column: rest + 1 };
        rest -= len;
      }
      return {
        lineNumber: lines.length,
        column: lines[lines.length - 1]!.length + 1,
      };
    },
  };
}

function fakeMonaco() {
  const disposeCompletion = vi.fn();
  const registerCompletionItemProvider = vi.fn(() => ({
    dispose: disposeCompletion,
  }));
  const monaco = {
    languages: {
      registerCompletionItemProvider,
      registerHoverProvider: vi.fn(() => ({ dispose: vi.fn() })),
      CompletionItemKind: { Variable: KIND_VARIABLE },
      CompletionTriggerKind: { Invoke: 0, TriggerCharacter: 1 },
    },
  };
  return {
    monaco: monaco as unknown as typeof import('monaco-editor'),
    registerCompletionItemProvider,
    disposeCompletion,
  };
}

type Provider = {
  triggerCharacters: string[];
  provideCompletionItems(
    model: ReturnType<typeof fakeModel>,
    position: { lineNumber: number; column: number },
    context: { triggerKind: number }
  ): {
    suggestions: {
      label: string;
      detail: string;
      kind: number;
      insertText: string;
      filterText: string;
      sortText: string;
      documentation?: string;
      range: {
        startLineNumber: number;
        startColumn: number;
        endLineNumber: number;
        endColumn: number;
      };
    }[];
  };
};

describe('registerFieldReferenceCompletion', () => {
  const candidates: ReferenceCandidate[] = [
    { token: '${env.HOME}', label: 'HOME', group: 'env', known: 'static' },
    {
      token: '${params.API_URL}',
      label: 'API_URL',
      group: 'params',
      known: 'static',
    },
  ];
  const TRIGGER = 1;
  const INVOKE = 0;

  function setup(text: string) {
    const { monaco, registerCompletionItemProvider, disposeCompletion } =
      fakeMonaco();
    const model = fakeModel(text);
    const handle = registerFieldReferenceCompletion(
      monaco,
      { getModel: () => model },
      'shell',
      () => candidates
    );
    const [language, provider] = registerCompletionItemProvider.mock
      .calls[0] as unknown as [string, Provider];
    const complete = (
      offset: number,
      triggerKind: number,
      target: ReturnType<typeof fakeModel> = model
    ) =>
      provider.provideCompletionItems(target, target.getPositionAt(offset), {
        triggerKind,
      }).suggestions;
    return { handle, language, complete, disposeCompletion };
  }

  it('offers the candidates after an open reference', () => {
    const text = 'echo ${pa}\n';
    const { language, complete } = setup(text);
    expect(language).toBe('shell');
    const items = complete(at(text, 'pa', 2), TRIGGER);
    expect(items.map((item) => item.insertText)).toEqual([
      '${env.HOME}',
      '${params.API_URL}',
    ]);
    // The range runs from the dollar sign over the closing brace.
    expect(items[0]?.range).toMatchObject({ startColumn: 6, endColumn: 11 });
  });

  it('offers them at the caret for an explicit request only', () => {
    const text = 'echo a\n';
    const { complete } = setup(text);
    expect(complete(5, TRIGGER)).toEqual([]);
    expect(complete(5, INVOKE)[0]?.range).toMatchObject({
      startColumn: 6,
      endColumn: 6,
    });
  });

  // Monaco reports a list that opens while a word is typed as Invoke, the same
  // as Ctrl+Space. Offering there lets Enter replace what the author typed.
  it.each([
    ['ls', 1],
    ['ls', 2],
    ['echo a', 6],
    ['echo $HOME', 6],
    ['echo $HOME', 10],
    ['grep foo$', 9],
    ['$', 1],
  ])('offers nothing while typing %s at %i', (text, offset) => {
    const { complete } = setup(text);
    expect(complete(offset, INVOKE)).toEqual([]);
    expect(complete(offset, TRIGGER)).toEqual([]);
  });

  // ${NAME} is shell syntax in a script, as it is in a `run` field of the YAML.
  it('adds the shell note to env items', () => {
    const { complete } = setup('${');
    const [env, param] = complete(2, TRIGGER);
    expect(env?.documentation).toContain('${env.NAME}');
    expect(param?.documentation).toBeUndefined();
  });

  // Monaco providers are global to a language: other fields must not get these.
  it('offers nothing for the model of another editor', () => {
    const { complete } = setup('${');
    expect(complete(2, TRIGGER)).not.toEqual([]);
    expect(complete(2, TRIGGER, fakeModel('${'))).toEqual([]);
  });

  it('removes the provider on dispose', () => {
    const { handle, disposeCompletion } = setup('');
    handle.dispose();
    expect(disposeCompletion).toHaveBeenCalledTimes(1);
  });
});

describe('registerReferenceCompletion', () => {
  afterEach(() => {
    document.documentElement.lang = 'en';
  });

  function setup(text: string, offset: number, triggerKind: number) {
    const { monaco, registerCompletionItemProvider, disposeCompletion } =
      fakeMonaco();
    const handle = registerReferenceCompletion(monaco, () => ({
      scope: scopeHttpJq,
    }));
    const [language, provider] = registerCompletionItemProvider.mock
      .calls[0] as unknown as [string, Provider];
    const model = fakeModel(text);
    const result = provider.provideCompletionItems(
      model,
      model.getPositionAt(offset),
      {
        triggerKind,
      }
    );
    return { handle, language, provider, result, disposeCompletion };
  }

  it('registers a YAML provider with the trigger characters', () => {
    const { language, provider, handle, disposeCompletion } = setup(
      yamlHttpJq,
      0,
      0
    );
    expect(language).toBe('yaml');
    expect(provider.triggerCharacters).toEqual(['{', '.']);
    handle.dispose();
    expect(disposeCompletion).toHaveBeenCalledTimes(1);
  });

  it('builds items in candidate order for an explicit request', () => {
    const offset = at(yamlHttpJq, "'[.[]", 3);
    const { result } = setup(yamlHttpJq, offset, 0);
    const [first, second] = result.suggestions;
    expect(first).toMatchObject({
      label: '${steps.fetch.outputs.body}',
      insertText: '${steps.fetch.outputs.body}',
      filterText: '${steps.fetch.outputs.body}',
      kind: KIND_VARIABLE,
      sortText: '00000',
    });
    expect(first?.detail).toContain(' - ');
    expect(second?.sortText).toBe('00001');
    expect(first?.range).toMatchObject({
      startLineNumber: 17,
      endLineNumber: 17,
    });
    expect(first?.range.startColumn).toBe(first?.range.endColumn);
  });

  it('answers a trigger character only inside an open reference', () => {
    const offset = at(yamlHttpJq, "'[.[]", 3);
    expect(setup(yamlHttpJq, offset, 1).result.suggestions).toEqual([]);
    const text = `${yamlHttpJq.slice(0, offset)}\${${yamlHttpJq.slice(offset)}`;
    const { result } = setup(text, offset + 2, 1);
    expect(result.suggestions[0]?.label).toBe('${steps.fetch.outputs.body}');
    expect(result.suggestions[0]?.range.endColumn).toBe(
      result.suggestions[0]!.range.startColumn + 2
    );
  });

  // Monaco reports a list that opens while a word is typed as Invoke.
  it.each([
    ['steps:\n  - run: ls\n', 'ls', 2],
    ['steps:\n  - run: echo $HOME\n', '$HOME', 5],
    ['steps:\n  - run: grep foo$\n', 'foo$', 4],
  ])('offers nothing while typing in %j', (text, marker, shift) => {
    const { result } = setup(text, at(text, marker, shift), 0);
    expect(result.suggestions).toEqual([]);
  });

  it('adds a shell note to env items in a run field only', () => {
    const run = at(yamlArraySteps, 'sort data', 0);
    const inRun = setup(yamlArraySteps, run, 0).result.suggestions;
    const env = inRun.find((s) => s.label === '${env.HOME}');
    expect(env?.documentation).toBe(
      '${NAME} is shell syntax in run. Use ${env.NAME} for Dagu environment references.'
    );
    expect(
      inRun.find((s) => s.label === '${params.API_URL}')?.documentation
    ).toBeUndefined();
    const outside = setup(yamlHttpJq, at(yamlHttpJq, "'[.[]", 3), 0).result
      .suggestions;
    expect(
      outside.find((s) => s.label === '${env.HOME}')?.documentation
    ).toBeUndefined();
  });
});

describe('editorLocale', () => {
  afterEach(() => {
    document.documentElement.lang = 'en';
  });

  it('maps the document language to a supported locale', () => {
    document.documentElement.lang = 'zh-CN';
    expect(editorLocale()).toBe('zh-CN');
    document.documentElement.lang = 'ja';
    expect(editorLocale()).toBe('ja');
    document.documentElement.lang = 'fr';
    expect(editorLocale()).toBe('en');
  });
});
