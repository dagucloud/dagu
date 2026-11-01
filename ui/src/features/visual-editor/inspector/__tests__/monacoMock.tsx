// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useRef } from 'react';

/*
 * Stand-in for `@monaco-editor/react` in jsdom: a textarea that calls
 * `onChange` on input and the blur callback that `onMount` registers.
 * Use it with
 *   vi.mock('@monaco-editor/react', async () => (await import('./monacoMock')).monacoModule);
 *   vi.mock('../monacoSetup', () => ({ monacoTheme: () => 'light' }));
 */
function MockEditor(props: {
  value?: string;
  language?: string;
  onChange?(value: string | undefined): void;
  onMount?(editor: unknown, monaco: unknown): void;
  options?: { readOnly?: boolean; ariaLabel?: string };
}) {
  const blur = useRef<(() => void) | null>(null);
  const mounted = useRef(false);
  const wantsFocus = useRef(false);
  if (!mounted.current) {
    mounted.current = true;
    props.onMount?.(
      {
        onDidBlurEditorText(callback: () => void) {
          blur.current = callback;
          return { dispose() {} };
        },
        // The real editor focuses once it is in the page: the textarea does.
        focus() {
          wantsFocus.current = true;
        },
      },
      {}
    );
  }
  return (
    <textarea
      data-testid="monaco"
      ref={(element) => {
        if (element && wantsFocus.current) {
          wantsFocus.current = false;
          element.focus();
        }
      }}
      data-language={props.language}
      aria-label={props.options?.ariaLabel}
      readOnly={props.options?.readOnly}
      value={props.value ?? ''}
      onChange={(event) => props.onChange?.(event.target.value)}
      onBlur={() => blur.current?.()}
    />
  );
}

export const monacoModule = {
  default: MockEditor,
  loader: { config() {} },
};
