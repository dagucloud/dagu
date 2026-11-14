// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { lazy, Suspense, useRef, type ReactElement } from 'react';
import { fieldEditorHeight, type MonacoFieldProps } from './monacoFieldProps';

export type { MonacoFieldProps } from './monacoFieldProps';

const loadFieldEditor = () => import('./MonacoFieldEditor');

/** Starts the load of the editor, for example when a pointer nears it. */
export function preloadMonacoField(): void {
  void loadFieldEditor();
}

const FieldEditor = lazy(() =>
  loadFieldEditor().then((module) => ({ default: module.MonacoFieldEditor }))
);

/**
 * Small Monaco editor for one field. Monaco loads when the first field
 * shows, and an empty frame of the same size holds its place until then.
 */
export function MonacoField(props: MonacoFieldProps): ReactElement {
  const { lines = 10, ariaLabel } = props;
  // A host that focuses the field before Monaco loads reaches the frame: the
  // editor takes the focus when it mounts.
  const focusRequest = useRef(false);
  return (
    <Suspense
      fallback={
        <div
          tabIndex={-1}
          data-monaco-pending=""
          className="overflow-hidden rounded border border-border outline-none"
          aria-label={ariaLabel}
          onFocus={() => {
            focusRequest.current = true;
          }}
          onBlur={(event) => {
            if (event.relatedTarget) focusRequest.current = false;
          }}
        >
          <div style={{ height: fieldEditorHeight(lines) }} />
        </div>
      }
    >
      <FieldEditor {...props} focusRequest={focusRequest} />
    </Suspense>
  );
}
