// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { editor, IDisposable, languages } from 'monaco-editor';

type Monaco = typeof import('monaco-editor');

/** A step of the text: the line of its first row and its server name. */
export interface StepLensPoint {
  line: number;
  name: string;
}

/** What the lenses of a whole-file editor need of their host. */
export interface StepLensSource {
  /** The steps of the text, in order. */
  steps: readonly StepLensPoint[];
  /** The text of the lens. */
  label: string;
  /** Opens the step in the full-screen step view. */
  open(name: string): void;
}

/** The registration of the lenses of one editor. */
export interface StepLensRegistration extends IDisposable {
  /** Draws the lenses again: the steps or the label changed. */
  refresh(): void;
}

/**
 * Puts one lens, "Open step", above the first row of each step of the model
 * of `codeEditor`. The lens opens the step in the full-screen step view.
 * Dispose the result when the editor unmounts.
 */
export function registerStepLenses(
  monaco: Monaco,
  codeEditor: editor.IStandaloneCodeEditor,
  read: () => StepLensSource | null
): StepLensRegistration {
  const changed = new monaco.Emitter<languages.CodeLensProvider>();
  // The handler of the command receives the name from the lens.
  const commandId = codeEditor.addCommand(0, (_accessor, name: unknown) => {
    if (typeof name === 'string') read()?.open(name);
  });
  const provider = monaco.languages.registerCodeLensProvider('yaml', {
    onDidChange: changed.event,
    provideCodeLenses(model) {
      const source = read();
      if (!source || !commandId || model !== codeEditor.getModel()) {
        return { lenses: [], dispose() {} };
      }
      const lines = model.getLineCount();
      return {
        lenses: source.steps
          .filter((step) => step.line >= 1 && step.line <= lines)
          .map((step) => ({
            range: {
              startLineNumber: step.line,
              startColumn: 1,
              endLineNumber: step.line,
              endColumn: 1,
            },
            command: {
              id: commandId,
              title: source.label,
              arguments: [step.name],
            },
          })),
        dispose() {},
      };
    },
  });
  return {
    refresh: () => changed.fire({} as languages.CodeLensProvider),
    dispose() {
      provider.dispose();
      changed.dispose();
    },
  };
}
