// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** The Prompt card renders at most this many characters of the system message. */
export const SYSTEM_PREVIEW_CHARS = 160;
/** The Prompt card renders at most this many characters of the prompt. */
export const PROMPT_PREVIEW_CHARS = 320;
/** The Prompt card renders at most this many lines of a text. */
const PREVIEW_LINES = 8;

/**
 * How much room the texts need in the Prompt card: the lines of the system
 * message (1 or 2) and of the prompt (2 or 4). The card has two heights for
 * each text, so that a short text leaves no empty block and a typed character
 * moves the layout at most once for each text.
 */
export interface PromptFit {
  system: 1 | 2;
  prompt: 2 | 4;
}

/** The fit of a card with long texts: the most that the card shows. */
export const PROMPT_FULL: PromptFit = { system: 2, prompt: 4 };

// Width of a character in em: a wide character (CJK, fullwidth, emoji) is a square.
function charEm(code: number): number {
  const wide =
    (code >= 0x1100 && code <= 0x115f) ||
    (code >= 0x2e80 && code <= 0xa4cf) ||
    (code >= 0xac00 && code <= 0xd7a3) ||
    (code >= 0xf900 && code <= 0xfaff) ||
    (code >= 0xfe30 && code <= 0xfe6f) ||
    (code >= 0xff00 && code <= 0xff60) ||
    (code >= 0x1f300 && code <= 0x1faff);
  return wide ? 1 : 0.58;
}

const LIST_OR_HEADING = /^\s*([-*+]\s|\d+[.)]\s|#{1,6}\s)/;

/**
 * An estimate of the lines that a text takes in `width` px at `font` px, as
 * Markdown. The estimate is a little high, so that the card keeps the room
 * of the text; the preview cuts a text that is still longer.
 */
export function previewLines(text: string, width: number, font: number) {
  const em = (line: string) =>
    Array.from(line).reduce((sum, ch) => sum + charEm(ch.codePointAt(0)!), 0) *
    font;
  let lines = 0;
  for (const paragraph of text.trim().split(/\n\s*\n/)) {
    // The lines of a paragraph join, except for the items of a list and the headings.
    const logical: string[] = [];
    for (const line of paragraph.split('\n')) {
      if (logical.length === 0 || LIST_OR_HEADING.test(line)) {
        logical.push(line.trim());
      } else {
        logical[logical.length - 1] += ` ${line.trim()}`;
      }
    }
    for (const line of logical) {
      if (line !== '') lines += Math.max(1, Math.ceil(em(line) / width));
    }
  }
  return lines;
}

/** Width of the text of the Prompt card in px: the card without its padding and border. */
const PROMPT_TEXT_WIDTH = 238;
const PROMPT_FONT = 11;

/**
 * The fit of the Prompt card for a system message and a prompt. It reckons
 * with the font of the normal card, so that both sizes of the canvas have the
 * same fit: a compact card has more room than it needs.
 */
export function promptFit(system: string, prompt: string): PromptFit {
  const lines = (text: string, max: number) =>
    previewLines(previewText(text, max), PROMPT_TEXT_WIDTH, PROMPT_FONT);
  return {
    system: lines(system, SYSTEM_PREVIEW_CHARS) > 1 ? 2 : 1,
    prompt: lines(prompt, PROMPT_PREVIEW_CHARS) > 2 ? 4 : 2,
  };
}

/**
 * The first characters of a text, for the Prompt card. The card renders the
 * result as Markdown, so the cut keeps whole lines where it can: it takes at
 * most `max` characters and `PREVIEW_LINES` lines, and ends with an ellipsis
 * when it left text out. The text is trimmed first.
 */
export function previewText(text: string, max: number): string {
  const trimmed = text.trim();
  const lines = trimmed.split('\n');
  let cut = lines.slice(0, PREVIEW_LINES).join('\n');
  let left = lines.length > PREVIEW_LINES;
  if (cut.length > max) {
    cut = cut.slice(0, max).trimEnd();
    left = true;
  }
  return left ? `${cut}…` : cut;
}
