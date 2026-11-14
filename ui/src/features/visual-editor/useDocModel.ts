// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useMemo, useState } from 'react';
import type { DagDocModel } from './contracts';
import { parseDagDocLenient } from './doc';

/** Delay before typed text is parsed again. */
export const DOC_MODEL_DELAY_MS = 250;

/** Delay while the canvas shows the last valid model: a fix shows at once. */
export const FROZEN_MODEL_DELAY_MS = 40;

export interface DocModelState {
  /**
   * Model of the last text that parsed; the current one when it parses or
   * when no text has parsed yet.
   */
  model: DagDocModel;
  /** True while the parsed text has YAML syntax errors. */
  frozen: boolean;
  /** Parses `text` at once, for text that the visual editor wrote itself. */
  sync(text: string): void;
}

/**
 * Parses the DAG text into a document model. Typed text is parsed after a
 * short delay; `sync` skips the delay. Entries of `steps` that have a YAML
 * error of their own are placeholders and the other steps stay. While the
 * rest of the text does not parse, the model of the last text that parsed
 * stays, and `frozen` is true. Until a text parses, the model is the current
 * one, with `ok` false.
 */
export function useDocModel(
  text: string,
  delayMs: number = DOC_MODEL_DELAY_MS
): DocModelState {
  const [parsedText, setParsedText] = useState(text);
  const parsed = useMemo(() => parseDagDocLenient(parsedText), [parsedText]);
  const settled = parsed.ok && parsed.parseErrors.length === 0;
  const wait = settled ? delayMs : Math.min(delayMs, FROZEN_MODEL_DELAY_MS);

  useEffect(() => {
    if (text === parsedText) {
      return;
    }
    const timer = window.setTimeout(() => setParsedText(text), wait);
    return () => window.clearTimeout(timer);
  }, [text, parsedText, wait]);

  // Adjusted during render, so the frozen model never lags one render.
  const [lastGood, setLastGood] = useState(parsed);
  if (parsed.ok && parsed !== lastGood) {
    setLastGood(parsed);
  }

  const sync = useCallback((next: string) => setParsedText(next), []);

  return {
    model: parsed.ok || !lastGood.ok ? parsed : lastGood,
    frozen: !parsed.ok,
    sync,
  };
}
