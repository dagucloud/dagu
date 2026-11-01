// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { Plus } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ReferenceCandidate, ReferenceInputProps } from '../contracts';
import { ReferencePicker, type ReferencePickerHandle } from './ReferencePicker';
import {
  findOpenReference,
  findReferenceTokens,
  insertReference,
  type ReferenceTokenKind,
} from './tokens';

type FieldElement = HTMLInputElement | HTMLTextAreaElement;

const PICKER_KEYS = new Set(['ArrowDown', 'ArrowUp', 'Enter', 'Escape']);

// The overlay copies these classes, so the chips line up with the field text.
const BOX_CLASS = 'border px-2 pr-8 text-xs';
const SINGLE_CLASS = 'h-7 py-0 leading-[26px]';
const MULTI_CLASS = 'min-h-16 py-1 leading-5';

function chipClass(kind: ReferenceTokenKind): string {
  switch (kind) {
    case 'canonical':
      return 'rounded-[3px] bg-primary/15 text-primary';
    case 'unsupported':
      return 'rounded-[3px] bg-warning/10 text-warning';
    default:
      return 'rounded-[3px] bg-muted text-muted-foreground';
  }
}

/** Text field that inserts references, from a picker or from typing "${". The value is a plain string. */
export function ReferenceInput({
  value,
  onChange,
  candidates,
  multiline,
  placeholder,
  disabled,
  id,
  'aria-label': ariaLabel,
  onBlur,
}: ReferenceInputProps): ReactElement {
  const { ts } = useI18n();
  const listboxId = `${useId()}-references`;
  const wrapperRef = useRef<HTMLDivElement>(null);
  const fieldRef = useRef<FieldElement | null>(null);
  const overlayRef = useRef<HTMLDivElement | null>(null);
  const pickerRef = useRef<ReferencePickerHandle>(null);
  const selectionRef = useRef({ start: 0, end: 0 });
  const pendingRef = useRef<{ text: string; caret: number } | null>(null);

  const [focused, setFocused] = useState(false);
  const [caret, setCaret] = useState(value.length);
  const [buttonOpen, setButtonOpen] = useState(false);
  const [dismissedFrom, setDismissedFrom] = useState<number | null>(null);
  const [activeId, setActiveId] = useState<string | undefined>();

  const openReference =
    !disabled && focused && !buttonOpen
      ? findOpenReference(value, Math.min(caret, value.length))
      : null;
  const inlineOpen =
    openReference !== null && openReference.from !== dismissedFrom;
  const pickerOpen = !disabled && (buttonOpen || inlineOpen);

  useEffect(() => {
    if (openReference === null && dismissedFrom !== null)
      setDismissedFrom(null);
  }, [openReference, dismissedFrom]);

  useEffect(() => {
    if (disabled) setButtonOpen(false);
  }, [disabled]);

  // Put the caret after an inserted token once the new value is in the DOM.
  useLayoutEffect(() => {
    const pending = pendingRef.current;
    const el = fieldRef.current;
    if (!pending || !el || el.value !== pending.text) return;
    pendingRef.current = null;
    el.setSelectionRange(pending.caret, pending.caret);
    setCaret(pending.caret);
  });

  const tokens = useMemo(() => findReferenceTokens(value), [value]);
  const showChips = !focused && !buttonOpen && tokens.length > 0;

  const syncCaret = (el: FieldElement) =>
    setCaret(el.selectionStart ?? el.value.length);

  // The chips stand over the text of the field: they keep its scroll offset.
  const syncOverlay = () => {
    const overlay = overlayRef.current;
    const el = fieldRef.current;
    if (!overlay || !el) return;
    overlay.scrollTop = el.scrollTop;
    overlay.scrollLeft = el.scrollLeft;
  };

  const apply = (result: { text: string; caret: number }) => {
    pendingRef.current = result;
    setCaret(result.caret);
    onChange(result.text);
    fieldRef.current?.focus();
  };

  const pickInline = (candidate: ReferenceCandidate) => {
    const el = fieldRef.current;
    if (!el) return;
    const pos = Math.min(el.selectionStart ?? value.length, value.length);
    const open = findOpenReference(value, pos);
    if (!open) return;
    // Inside a closed token, the pick replaces the whole token.
    const closed = findReferenceTokens(value).find(
      (t) => t.from === open.from && pos < t.to
    );
    apply(
      insertReference(value, open.from, closed?.to ?? pos, candidate.token)
    );
  };

  const pickFromButton = (candidate: ReferenceCandidate) => {
    const start = Math.min(selectionRef.current.start, value.length);
    const end = Math.min(
      Math.max(selectionRef.current.end, start),
      value.length
    );
    apply(insertReference(value, start, end, candidate.token));
    setButtonOpen(false);
  };

  const closeButtonPicker = () => {
    setButtonOpen(false);
    fieldRef.current?.focus();
  };

  const toggleButtonPicker = () => {
    if (disabled) return;
    if (buttonOpen) {
      closeButtonPicker();
      return;
    }
    const el = fieldRef.current;
    selectionRef.current = {
      start: el?.selectionStart ?? value.length,
      end: el?.selectionEnd ?? value.length,
    };
    setButtonOpen(true);
  };

  const fieldProps = {
    id,
    value,
    placeholder,
    disabled,
    'aria-label': ariaLabel,
    // A text field, not a combobox: aria-expanded and aria-haspopup do not
    // apply to it. The open list is announced through aria-controls.
    'aria-autocomplete': 'list' as const,
    'aria-controls': inlineOpen ? listboxId : undefined,
    'aria-activedescendant': inlineOpen ? activeId : undefined,
    className: cn(
      'block w-full min-w-0 rounded-md border-input bg-card text-foreground shadow-sm transition-colors',
      'placeholder:text-muted-foreground',
      'focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/20',
      'disabled:cursor-not-allowed disabled:bg-muted disabled:opacity-50',
      BOX_CLASS,
      multiline ? cn(MULTI_CLASS, 'resize-none') : SINGLE_CLASS,
      showChips && 'text-transparent'
    ),
    onChange: (e: { target: FieldElement }) => {
      pendingRef.current = null;
      syncCaret(e.target);
      onChange(e.target.value);
    },
    onFocus: (e: { currentTarget: FieldElement }) => {
      setFocused(true);
      // The button picker gives the keys back to the field.
      setButtonOpen(false);
      syncCaret(e.currentTarget);
    },
    onBlur: (e: { currentTarget: FieldElement }) => {
      setFocused(false);
      e.currentTarget.scrollLeft = 0;
    },
    onScroll: syncOverlay,
    onSelect: (e: { currentTarget: FieldElement }) =>
      syncCaret(e.currentTarget),
    onKeyUp: (e: { currentTarget: FieldElement }) => syncCaret(e.currentTarget),
    onClick: (e: { currentTarget: FieldElement }) => syncCaret(e.currentTarget),
    onKeyDown: (e: {
      key: string;
      preventDefault(): void;
      stopPropagation(): void;
    }) => {
      if (!inlineOpen || !PICKER_KEYS.has(e.key)) return;
      if (pickerRef.current?.handleKey(e.key)) {
        e.preventDefault();
        e.stopPropagation();
      }
    },
  };

  const overlay: ReactNode[] = [];
  if (showChips) {
    let pos = 0;
    tokens.forEach((token, index) => {
      if (token.from > pos) overlay.push(value.slice(pos, token.from));
      overlay.push(
        <span
          key={index}
          data-reference-chip={token.kind}
          className={chipClass(token.kind)}
        >
          {token.text}
        </span>
      );
      pos = token.to;
    });
    if (pos < value.length) overlay.push(value.slice(pos));
  }

  return (
    <div
      ref={wrapperRef}
      className="relative"
      onBlur={(e) => {
        if (wrapperRef.current?.contains(e.relatedTarget as Node | null))
          return;
        setButtonOpen(false);
        onBlur?.();
      }}
    >
      {multiline ? (
        <textarea
          {...fieldProps}
          ref={(el) => {
            fieldRef.current = el;
          }}
          rows={3}
        />
      ) : (
        <input
          {...fieldProps}
          ref={(el) => {
            fieldRef.current = el;
          }}
          type="text"
        />
      )}
      {showChips ? (
        <div
          ref={(el) => {
            overlayRef.current = el;
            syncOverlay();
          }}
          aria-hidden="true"
          className={cn(
            'pointer-events-none absolute inset-0 overflow-hidden border-transparent text-foreground',
            BOX_CLASS,
            multiline
              ? cn(MULTI_CLASS, 'whitespace-pre-wrap break-words')
              : cn(SINGLE_CLASS, 'whitespace-pre'),
            disabled && 'opacity-50'
          )}
        >
          {overlay}
        </div>
      ) : null}
      <button
        type="button"
        aria-label={ts('Insert reference')}
        disabled={disabled}
        // Keep the focus in the field: the picker takes it on open.
        onMouseDown={(e) => e.preventDefault()}
        onClick={toggleButtonPicker}
        className="absolute right-0 top-0 inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/20 disabled:cursor-not-allowed disabled:opacity-50"
      >
        <Plus className="h-3.5 w-3.5" aria-hidden="true" />
      </button>
      {pickerOpen ? (
        <ReferencePicker
          key={buttonOpen ? 'button' : 'inline'}
          ref={pickerRef}
          id={listboxId}
          candidates={candidates}
          showSearch={buttonOpen}
          query={buttonOpen ? undefined : openReference?.query}
          onPick={buttonOpen ? pickFromButton : pickInline}
          onClose={
            buttonOpen
              ? closeButtonPicker
              : () => setDismissedFrom(openReference?.from ?? null)
          }
          onActiveChange={setActiveId}
        />
      ) : null}
    </div>
  );
}
