// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import MonacoEditor from '@monaco-editor/react';
import { useEffect, useRef, useState, type ReactElement } from 'react';
import {
  registerFieldReferenceCompletion,
  registerStepYamlCompletion,
} from '../references/monacoCompletion';
import { monacoTheme } from './monacoSetup';
import {
  fieldEditorHeight,
  type MonacoFieldEditorProps,
} from './monacoFieldProps';

/**
 * Small Monaco editor for one field. It keeps a local draft and sends it
 * on blur when it differs from `value`. The draft resets when `value`
 * changes from outside. Given candidates, it completes references in its
 * text. The chunk of this module holds Monaco: MonacoField loads it on first
 * use.
 */
export function MonacoFieldEditor({
  value,
  language,
  readOnly,
  lines = 10,
  ariaLabel,
  candidates,
  candidatesAtPath,
  onChange,
  onCommit,
  focusRequest,
}: MonacoFieldEditorProps): ReactElement {
  const [draft, setDraft] = useState(value);
  const draftRef = useRef(value);
  const shown = useRef(value);
  const commitRef = useRef(onCommit);
  commitRef.current = onCommit;
  const valueRef = useRef(value);
  valueRef.current = value;
  const readOnlyRef = useRef(readOnly);
  readOnlyRef.current = readOnly;
  const candidatesRef = useRef(candidates);
  candidatesRef.current = candidates;
  const candidatesAtPathRef = useRef(candidatesAtPath);
  candidatesAtPathRef.current = candidatesAtPath;

  useEffect(() => {
    if (value !== shown.current) {
      shown.current = value;
      draftRef.current = value;
      setDraft(value);
    }
  }, [value]);

  return (
    <div
      className="overflow-hidden rounded border border-border"
      aria-label={ariaLabel}
    >
      <MonacoEditor
        height={fieldEditorHeight(lines)}
        language={language}
        theme={monacoTheme()}
        value={draft}
        onChange={(text) => {
          const next = text ?? '';
          draftRef.current = next;
          setDraft(next);
          onChange?.(next);
        }}
        onMount={(editor, monaco) => {
          if (focusRequest?.current) {
            focusRequest.current = false;
            editor.focus();
          }
          const completion = candidatesAtPathRef.current
            ? registerStepYamlCompletion(
                monaco,
                editor,
                (path) => candidatesAtPathRef.current?.(path) ?? []
              )
            : candidatesRef.current &&
              registerFieldReferenceCompletion(
                monaco,
                editor,
                language,
                () => candidatesRef.current ?? []
              );
          if (completion) {
            editor.onDidDispose(() => completion.dispose());
          }
          editor.onDidBlurEditorText(() => {
            if (!readOnlyRef.current && draftRef.current !== valueRef.current) {
              commitRef.current?.(draftRef.current);
            }
          });
        }}
        options={{
          readOnly,
          minimap: { enabled: false },
          lineNumbers: 'off',
          folding: false,
          scrollBeyondLastLine: false,
          wordWrap: 'on',
          fontSize: 12,
          automaticLayout: true,
          ariaLabel,
          // The frame of the field clips its overflow: the suggest widget
          // draws in the body.
          fixedOverflowWidgets: true,
        }}
      />
    </div>
  );
}
