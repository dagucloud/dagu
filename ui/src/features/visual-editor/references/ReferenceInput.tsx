// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
} from 'react';
import { Plus } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ReferenceCandidate, ReferenceInputProps } from '../contracts';
import { highlightPrompt } from './promptHighlight';
import { ReferencePicker, type ReferencePickerHandle } from './ReferencePicker';
import { filterSuggestions } from './suggestions';
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
const MULTI_CLASS = 'py-1 leading-5';

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

// The text colour of a reference in a field that is being edited.
function tokenClass(kind: ReferenceTokenKind): string {
  return kind === 'unsupported' ? 'text-warning' : 'text-info';
}

/** Text field that inserts references, from a picker or from typing "${". The value is a plain string. */
export function ReferenceInput({
  value,
  onChange,
  candidates,
  multiline,
  rows = 3,
  resizable = false,
  placeholder,
  disabled,
  id,
  'aria-label': ariaLabel,
  suggestions,
  suggestionsLabel,
  highlight = false,
  'aria-invalid': ariaInvalid,
  onBlur,
  onKeyDown,
}: ReferenceInputProps): ReactElement {
  const { ts } = useI18n();
  const baseId = useId();
  const listboxId = `${baseId}-references`;
  const suggestionsId = `${baseId}-suggestions`;
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
  // A command can end in "$", as in `grep foo$`: Enter stays with the field.
  const loneDollar =
    openReference !== null && value[openReference.from + 1] !== '{';

  // Plain-text suggestions. The text of the field stays free: nothing checks it
  // against the list, and a value that the list lacks is valid.
  const [suggestionActive, setSuggestionActive] = useState(-1);
  const [suggestionsDismissed, setSuggestionsDismissed] = useState(false);
  const shownSuggestions = useMemo(
    () => filterSuggestions(suggestions, value),
    [suggestions, value]
  );
  const suggestionsOpen =
    !disabled &&
    focused &&
    !pickerOpen &&
    !suggestionsDismissed &&
    shownSuggestions.length > 0;
  const suggestionId = (index: number) => `${suggestionsId}-${index}`;

  useEffect(() => {
    setSuggestionActive(-1);
  }, [value]);

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

  // The chips copy the text of the field: follow its scroll position.
  const followScroll = () => {
    const overlay = overlayRef.current;
    if (overlay && fieldRef.current) {
      overlay.scrollTop = fieldRef.current.scrollTop;
    }
  };
  useLayoutEffect(followScroll);

  const tokens = useMemo(() => findReferenceTokens(value), [value]);
  const showChips = !focused && !buttonOpen && tokens.length > 0;
  // A text with a light highlight (references, Markdown marks) shows through
  // the layer, also while it is edited: the field then draws no text itself.
  const layered = highlight && multiline === true;
  const showOverlay = layered || showChips;
  const segments = useMemo(
    () => (layered ? highlightPrompt(value) : null),
    [layered, value]
  );

  // The layer has the width of the text area of the field: a scroll bar of the
  // field takes some of it, so the layer takes the same width as padding.
  const [gutter, setGutter] = useState(0);
  const measureRef = useRef<() => void>(() => undefined);
  measureRef.current = () => {
    const el = fieldRef.current;
    if (!layered || !el) return;
    const bar = Math.max(
      0,
      el.offsetWidth - el.clientWidth - 2 * el.clientLeft
    );
    setGutter((last) => (last === bar ? last : bar));
  };
  useLayoutEffect(() => measureRef.current());
  useEffect(() => {
    const el = fieldRef.current;
    if (!layered || !el || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => measureRef.current());
    observer.observe(el);
    return () => observer.disconnect();
  }, [layered]);

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
    candidate.onPicked?.();
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
    candidate.onPicked?.();
    apply(insertReference(value, start, end, candidate.token));
    setButtonOpen(false);
  };

  const pickSuggestion = (name: string) => {
    apply({ text: name, caret: name.length });
    setSuggestionsDismissed(true);
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
    'aria-invalid': ariaInvalid,
    // A text field, not a combobox: aria-expanded and aria-haspopup do not
    // apply to it. The open list is announced through aria-controls.
    'aria-autocomplete': 'list' as const,
    'aria-controls': inlineOpen
      ? listboxId
      : suggestionsOpen
        ? suggestionsId
        : undefined,
    'aria-activedescendant': inlineOpen
      ? activeId
      : suggestionsOpen && suggestionActive >= 0
        ? suggestionId(suggestionActive)
        : undefined,
    className: cn(
      'block w-full min-w-0 rounded-md border-input bg-card text-foreground shadow-sm transition-colors',
      'placeholder:text-muted-foreground',
      'focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/20',
      'disabled:cursor-not-allowed disabled:bg-muted disabled:opacity-50',
      BOX_CLASS,
      multiline
        ? cn(MULTI_CLASS, resizable ? 'resize-y' : 'resize-none')
        : SINGLE_CLASS,
      showOverlay && 'text-transparent',
      // The field draws the selection under the text of the layer.
      layered && 'selection:bg-primary/20'
    ),
    style: layered ? { caretColor: 'var(--foreground)' } : undefined,
    onChange: (e: { target: FieldElement }) => {
      pendingRef.current = null;
      setSuggestionsDismissed(false);
      syncCaret(e.target);
      onChange(e.target.value);
    },
    onFocus: (e: { currentTarget: FieldElement }) => {
      setFocused(true);
      setSuggestionsDismissed(false);
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
    onKeyDown: (e: KeyboardEvent<FieldElement>) => {
      if (suggestionsOpen) {
        const count = shownSuggestions.length;
        const picked = shownSuggestions[suggestionActive];
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          const step = e.key === 'ArrowDown' ? 1 : -1;
          setSuggestionActive(
            suggestionActive < 0
              ? step === 1
                ? 0
                : count - 1
              : (suggestionActive + step + count) % count
          );
        } else if (e.key === 'Enter' && picked !== undefined) {
          pickSuggestion(picked);
        } else if (e.key === 'Escape') {
          setSuggestionsDismissed(true);
        } else {
          return;
        }
        e.preventDefault();
        e.stopPropagation();
        return;
      }
      const forPicker =
        inlineOpen && !e.nativeEvent.isComposing && PICKER_KEYS.has(e.key);
      if (forPicker && pickerRef.current?.handleKey(e.key)) {
        e.preventDefault();
        e.stopPropagation();
        return;
      }
      onKeyDown?.(e);
    },
  };

  const overlay: ReactNode[] = [];
  if (segments) {
    segments.forEach((segment, index) => {
      if (segment.kind === 'mark') {
        overlay.push(
          <span key={index} className="text-muted-foreground">
            {segment.text}
          </span>
        );
      } else if (segment.kind === 'ref' && segment.token) {
        overlay.push(
          showChips ? (
            <span
              key={index}
              data-reference-chip={segment.token.kind}
              className={chipClass(segment.token.kind)}
            >
              {segment.text}
            </span>
          ) : (
            <span
              key={index}
              data-reference-token={segment.token.kind}
              className={tokenClass(segment.token.kind)}
            >
              {segment.text}
            </span>
          )
        );
      } else {
        overlay.push(segment.text);
      }
    });
    // A last line break needs a character after it to take a line.
    overlay.push('\u200b');
  } else if (showChips) {
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
          rows={rows}
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
      {showOverlay ? (
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
          style={
            layered && gutter > 0
              ? { paddingRight: `calc(2rem + ${gutter}px)` }
              : undefined
          }
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
      {suggestionsOpen ? (
        <div
          className="absolute left-0 top-full z-50 mt-1 w-full min-w-64 overflow-hidden rounded-md border border-border bg-popover text-popover-foreground shadow-md"
          // Keep the focus in the field: a click on a name must not blur it.
          onMouseDown={(e) => e.preventDefault()}
        >
          <div
            id={suggestionsId}
            role="listbox"
            aria-label={suggestionsLabel}
            className="max-h-60 overflow-y-auto py-1"
          >
            {shownSuggestions.map((name, index) => (
              <div
                key={name}
                id={suggestionId(index)}
                role="option"
                aria-selected={index === suggestionActive}
                onClick={() => pickSuggestion(name)}
                onMouseMove={() => setSuggestionActive(index)}
                className={cn(
                  'cursor-pointer truncate px-2 py-1 font-mono text-xs',
                  index === suggestionActive &&
                    'bg-accent text-accent-foreground'
                )}
              >
                {name}
              </div>
            ))}
          </div>
        </div>
      ) : null}
      {pickerOpen ? (
        <ReferencePicker
          key={buttonOpen ? 'button' : 'inline'}
          ref={pickerRef}
          id={listboxId}
          candidates={candidates}
          showSearch={buttonOpen}
          query={buttonOpen ? undefined : openReference?.query}
          idle={loneDollar}
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
