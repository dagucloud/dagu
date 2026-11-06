// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Braces } from 'lucide-react';
import {
  useId,
  useRef,
  useState,
  type ReactElement,
  type RefObject,
} from 'react';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import type { ReferenceCandidate } from '../contracts';
import {
  insertAtCaret,
  referencesAtCaret,
  type CaretEditor,
} from './editorCaret';
import type { ReferenceEditorState } from './editorState';
import { publishEdit } from './publishOutput';
import { ReferencePicker } from './ReferencePicker';

export interface YamlReferenceButtonProps {
  /** The YAML editor, read when the button is pressed. */
  editorRef: RefObject<unknown>;
  getState(): ReferenceEditorState;
}

/**
 * A toolbar button that lists what the field at the caret of a YAML editor can
 * read, for an author who does not know the names yet, and inserts the one they
 * pick at the caret.
 */
export function YamlReferenceButton({
  editorRef,
  getState,
}: YamlReferenceButtonProps): ReactElement {
  const { ts } = useI18n();
  const listboxId = `${useId()}-references`;
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [items, setItems] = useState<ReferenceCandidate[] | null>(null);
  const editor = () => editorRef.current as CaretEditor | null;

  const close = () => {
    setItems(null);
    editor()?.focus?.();
  };

  return (
    <div
      ref={wrapperRef}
      className="relative"
      onBlur={(event) => {
        if (!wrapperRef.current?.contains(event.relatedTarget as Node | null)) {
          setItems(null);
        }
      }}
    >
      <Button
        type="button"
        variant="ghost"
        className="h-7 w-7 p-0"
        aria-label={ts('Insert reference')}
        title={ts('Insert reference')}
        aria-expanded={items !== null}
        // The caret stays where the author left it.
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => {
          if (items) {
            close();
            return;
          }
          const current = editor();
          setItems(
            current ? (referencesAtCaret(current, getState()) ?? []) : []
          );
        }}
      >
        <Braces className="h-4 w-4" aria-hidden="true" />
      </Button>
      {items && (
        <ReferencePicker
          id={listboxId}
          candidates={items}
          showSearch
          align="end"
          emptyMessage={ts('Put the cursor in a field value')}
          onPick={(candidate) => {
            const current = editor();
            const text = current?.getModel()?.getValue();
            const published =
              candidate.publish && text !== undefined
                ? publishEdit(text, candidate.publish)
                : null;
            insertAtCaret(
              current,
              candidate.token,
              published?.ok ? published.edits[0] : undefined
            );
            setItems(null);
          }}
          onClose={close}
        />
      )}
    </div>
  );
}
