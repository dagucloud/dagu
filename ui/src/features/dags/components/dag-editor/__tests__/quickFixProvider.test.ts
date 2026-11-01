// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { editor, languages } from 'monaco-editor';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { ValidationIssue } from '@/features/visual-editor/contracts';
import { NO_ID_MESSAGE } from '@/features/visual-editor/doc/issueMessages';
import {
  parseDagDoc,
  resolveStepNames,
} from '@/features/visual-editor/doc/parse';
import { applyTextEdits } from '@/features/visual-editor/doc/patch';
import { ISSUE_MARKER_OWNER, buildIssueMarkers } from '../issueMarkers';
import {
  buildQuickFixActions,
  registerQuickFixSource,
  type QuickFixSource,
} from '../quickFixProvider';

const issue = (stepPath: string, message = NO_ID_MESSAGE): ValidationIssue => ({
  severity: ValidationIssueSeverity.warning,
  source: ValidationIssueSource.build,
  message,
  stepPath,
});

/** A model over `text`, with the offset conversion of Monaco. */
function fakeModel(text: string, uri = 'inmemory://dagu/editor/a.yaml') {
  const eol = text.includes('\r\n') ? '\r\n' : '\n';
  return {
    uri: { toString: () => uri },
    getValue: () => text,
    getVersionId: () => 3,
    getPositionAt(offset: number) {
      const before = text.slice(0, offset).split(eol);
      return {
        lineNumber: before.length,
        column: before[before.length - 1]!.length + 1,
      };
    },
  } as unknown as editor.ITextModel;
}

function sourceOf(text: string, issues: ValidationIssue[]): QuickFixSource {
  const model = parseDagDoc(text);
  return {
    getPlan: () => ({ model, issues, names: resolveStepNames(model) }),
  };
}

function markersOf(text: string, issues: ValidationIssue[]) {
  return buildIssueMarkers(text, issues, []);
}

const LF =
  'steps:\n  - name: show title\n    run: echo hi\n  - name: b\n    run: echo\n';
const CRLF = LF.replace(/\n/g, '\r\n');

/** The text after the workspace edit of an action. */
function applied(text: string, action: languages.CodeAction): string {
  const edits = (action.edit?.edits ?? []) as languages.IWorkspaceTextEdit[];
  const offsetOf = (line: number, column: number) => {
    const eol = text.includes('\r\n') ? '\r\n' : '\n';
    const lines = text.split(eol);
    return (
      lines.slice(0, line - 1).reduce((n, l) => n + l.length + eol.length, 0) +
      column -
      1
    );
  };
  return applyTextEdits(
    text,
    edits.map((e) => ({
      from: offsetOf(
        e.textEdit.range.startLineNumber,
        e.textEdit.range.startColumn
      ),
      to: offsetOf(e.textEdit.range.endLineNumber, e.textEdit.range.endColumn),
      insert: e.textEdit.text,
    }))
  );
}

describe('buildQuickFixActions', () => {
  it('gives the quick fix of a marker without id', () => {
    const issues = [issue('steps[0]')];
    const actions = buildQuickFixActions(
      fakeModel(LF),
      sourceOf(LF, issues),
      markersOf(LF, issues)
    );
    expect(actions).toHaveLength(1);
    const [action] = actions;
    expect(action).toMatchObject({
      title: 'Add id: show_title',
      kind: 'quickfix',
      isPreferred: true,
    });
    expect(action!.diagnostics).toEqual(markersOf(LF, issues));
    expect(applied(LF, action!)).toBe(
      'steps:\n  - name: show title\n    id: show_title\n    run: echo hi\n  - name: b\n    run: echo\n'
    );
  });

  // The owner's case: the other steps have an id, and the step has other
  // issues. The marker of the missing id is one of several on the step.
  it('gives the fix of a step without id when the others have one', () => {
    const text =
      'steps:\n  - name: first\n    id: first\n    run: echo\n  - name: second\n    run: echo\n  - name: third\n    id: third\n    run: echo\n';
    const issues = [
      { ...issue('steps[1]'), message: 'The action is unknown.' },
      issue('steps[1]'),
    ];
    const markers = markersOf(text, issues);
    expect(markers).toHaveLength(2);
    // Hidden areas of the step view do not change the text of the model.
    const actions = buildQuickFixActions(
      fakeModel(text),
      sourceOf(text, issues),
      markers
    );
    expect(actions.map((action) => action.title)).toEqual(['Add id: second']);
    expect(applied(text, actions[0]!)).toContain(
      '  - name: second\n    id: second\n    run: echo'
    );
  });

  it('gives no action for a marker without a fix', () => {
    const issues = [issue('steps[0]', 'The action is unknown.')];
    expect(
      buildQuickFixActions(
        fakeModel(LF),
        sourceOf(LF, issues),
        markersOf(LF, issues)
      )
    ).toEqual([]);
  });

  it('gives an action to fix all when there are several fixes', () => {
    const issues = [issue('steps[0]'), issue('steps[1]')];
    const markers = markersOf(LF, issues);
    const actions = buildQuickFixActions(fakeModel(LF), sourceOf(LF, issues), [
      markers[0]!,
    ]);
    expect(actions.map((a) => a.title)).toEqual([
      'Add id: show_title',
      'Fix all quick fixes in the file',
    ]);
    expect(applied(LF, actions[1]!)).toBe(
      'steps:\n  - name: show title\n    id: show_title\n    run: echo hi\n  - name: b\n    id: b\n    run: echo\n'
    );
  });

  it('gives no fix-all action for one fix', () => {
    const issues = [issue('steps[0]')];
    const actions = buildQuickFixActions(
      fakeModel(LF),
      sourceOf(LF, issues),
      markersOf(LF, issues)
    );
    expect(actions.map((a) => a.title)).not.toContain(
      'Fix all quick fixes in the file'
    );
  });

  it('makes ranges on the lines of an LF and a CRLF document', () => {
    for (const text of [LF, CRLF]) {
      const issues = [issue('steps[0]')];
      const [action] = buildQuickFixActions(
        fakeModel(text),
        sourceOf(text, issues),
        markersOf(text, issues)
      );
      const [edit] = action!.edit!.edits as languages.IWorkspaceTextEdit[];
      // The end of the `name:` line, in line and column.
      expect(edit!.textEdit.range).toEqual({
        startLineNumber: 2,
        startColumn: 21,
        endLineNumber: 2,
        endColumn: 21,
      });
      expect(edit!.versionId).toBe(3);
      expect(applied(text, action!)).toContain(
        text.includes('\r\n')
          ? 'show title\r\n    id: show_title\r\n    run'
          : 'show title\n    id: show_title\n    run'
      );
    }
  });

  it('gives nothing when the model is not the text of the plan', () => {
    const issues = [issue('steps[0]')];
    expect(
      buildQuickFixActions(
        fakeModel(`${LF}# edited\n`),
        sourceOf(LF, issues),
        markersOf(LF, issues)
      )
    ).toEqual([]);
  });

  it('gives nothing when the source has no plan', () => {
    expect(
      buildQuickFixActions(fakeModel(LF), { getPlan: () => null }, [
        {
          message: NO_ID_MESSAGE,
          severity: 4,
          startLineNumber: 2,
          startColumn: 5,
          endLineNumber: 2,
          endColumn: 6,
        },
      ])
    ).toEqual([]);
  });
});

describe('registerQuickFixSource', () => {
  // Registrations that a test leaves open would leak into the next test.
  const open: Array<{ dispose(): void }> = [];
  afterEach(() => {
    while (open.length > 0) open.pop()!.dispose();
  });
  const register = (
    ...args: Parameters<typeof registerQuickFixSource>
  ): { dispose(): void } => {
    const registration = registerQuickFixSource(...args);
    open.push(registration);
    return registration;
  };
  function fakeMonaco() {
    const providers: Array<{
      provider: languages.CodeActionProvider;
      dispose: ReturnType<typeof vi.fn>;
    }> = [];
    const commands: Array<{
      id: string;
      handler: (accessor: unknown, ...args: string[]) => void;
      dispose: ReturnType<typeof vi.fn>;
    }> = [];
    const models: editor.ITextModel[] = [];
    const monaco = {
      Uri: { parse: (value: string) => ({ toString: () => value }) },
      languages: {
        registerCodeActionProvider: vi.fn(
          (_lang: string, provider: languages.CodeActionProvider) => {
            const entry = { provider, dispose: vi.fn() };
            providers.push(entry);
            return entry;
          }
        ),
      },
      editor: {
        registerCommand: vi.fn(
          (
            id: string,
            handler: (accessor: unknown, ...args: string[]) => void
          ) => {
            const entry = { id, handler, dispose: vi.fn() };
            commands.push(entry);
            return entry;
          }
        ),
        getModels: () => models,
      },
    };
    return {
      monaco: monaco as unknown as typeof import('monaco-editor'),
      providers,
      commands,
      models,
    };
  }

  const context = (text: string, issues: ValidationIssue[]) =>
    ({
      markers: markersOf(text, issues).map((marker) => ({
        ...marker,
        owner: ISSUE_MARKER_OWNER,
      })),
      trigger: 1,
    }) as unknown as languages.CodeActionContext;

  it('serves the model of the source and no other model', () => {
    const { monaco, providers } = fakeMonaco();
    const issues = [issue('steps[0]')];
    const registration = register(
      monaco,
      'inmemory://dagu/editor/a.yaml',
      sourceOf(LF, issues)
    );
    const provide = providers[0]!.provider.provideCodeActions;
    const range = {} as never;
    const token = {} as never;
    const own = provide(
      fakeModel(LF),
      range,
      context(LF, issues),
      token
    ) as languages.CodeActionList;
    expect(own.actions).toHaveLength(1);
    // The run definition and the schema docs are other models.
    expect(
      provide(
        fakeModel(LF, 'inmemory://dagu/dag-runs/r1.yaml'),
        range,
        context(LF, issues),
        token
      )
    ).toBeNull();
    registration.dispose();
  });

  it('ignores the markers of other owners', () => {
    const { monaco, providers } = fakeMonaco();
    const issues = [issue('steps[0]')];
    register(monaco, 'inmemory://dagu/editor/a.yaml', sourceOf(LF, issues));
    const foreign = {
      markers: context(LF, issues).markers.map((m) => ({
        ...m,
        owner: 'yaml',
      })),
    } as unknown as languages.CodeActionContext;
    expect(
      providers[0]!.provider.provideCodeActions(
        fakeModel(LF),
        {} as never,
        foreign,
        {} as never
      )
    ).toBeNull();
  });

  it('shares one provider and disposes it with the last editor', () => {
    const { monaco, providers, commands } = fakeMonaco();
    const a = register(monaco, 'inmemory://a.yaml', sourceOf(LF, []));
    const b = register(monaco, 'inmemory://b.yaml', sourceOf(LF, []));
    expect(providers).toHaveLength(1);
    expect(commands).toHaveLength(1);
    a.dispose();
    expect(providers[0]!.dispose).not.toHaveBeenCalled();
    b.dispose();
    expect(providers[0]!.dispose).toHaveBeenCalledTimes(1);
    expect(commands[0]!.dispose).toHaveBeenCalledTimes(1);
    // A new editor registers a new provider.
    const c = register(monaco, 'inmemory://c.yaml', sourceOf(LF, []));
    expect(providers).toHaveLength(2);
    c.dispose();
  });

  it('offers nothing after the editor unmounts', () => {
    const { monaco, providers } = fakeMonaco();
    const issues = [issue('steps[0]')];
    const a = register(
      monaco,
      'inmemory://dagu/editor/a.yaml',
      sourceOf(LF, issues)
    );
    const b = register(
      monaco,
      'inmemory://dagu/editor/b.yaml',
      sourceOf(LF, [])
    );
    a.dispose();
    expect(
      providers[0]!.provider.provideCodeActions(
        fakeModel(LF),
        {} as never,
        context(LF, issues),
        {} as never
      )
    ).toBeNull();
    b.dispose();
  });

  it('reports the new text after an action is applied', () => {
    const { monaco, commands, models } = fakeMonaco();
    const onApplied = vi.fn();
    const registration = register(monaco, 'inmemory://dagu/editor/a.yaml', {
      ...sourceOf(LF, []),
      onApplied,
    });
    models.push(fakeModel('new text'));
    commands[0]!.handler(null, 'inmemory://dagu/editor/a.yaml');
    expect(onApplied).toHaveBeenCalledWith('new text');
    registration.dispose();
  });
});
