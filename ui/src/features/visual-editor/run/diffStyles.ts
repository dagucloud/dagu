// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Colours of the diff views (react-diff-viewer-continued), for the split and
// the unified view of every diff dialog. The theme tokens switch with the
// theme, so the dark and the light set are the same. All text is in the
// foreground token: only the backgrounds, the bar and the sign carry the
// colour of the change.

const tint = (token: string, percent: number) =>
  `color-mix(in srgb, var(--${token}) ${percent}%, transparent)`;

// The sign of a changed line is the token mixed with the foreground, so that
// it keeps 4.5:1 on the tint of the line in both themes.
const sign = (token: string) =>
  `color-mix(in srgb, var(--${token}) 70%, var(--foreground))`;

const added = 'success';
const removed = 'destructive';

const colours = {
  diffViewerBackground: 'var(--surface)',
  diffViewerColor: 'var(--foreground)',
  addedBackground: tint(added, 16),
  addedColor: 'var(--foreground)',
  removedBackground: tint(removed, 16),
  removedColor: 'var(--foreground)',
  // The line number of a changed line.
  addedGutterBackground: tint(added, 28),
  addedGutterColor: 'var(--foreground)',
  removedGutterBackground: tint(removed, 28),
  removedGutterColor: 'var(--foreground)',
  wordAddedBackground: tint(added, 38),
  wordRemovedBackground: tint(removed, 38),
  gutterBackground: 'var(--muted)',
  gutterBackgroundDark: 'var(--muted)',
  gutterColor: 'var(--muted-foreground)',
  // A blank or missing line is as plain as any unchanged line.
  emptyLineBackground: 'var(--surface)',
  codeFoldBackground: 'var(--muted)',
  codeFoldGutterBackground: 'var(--muted)',
  codeFoldContentColor: 'var(--muted-foreground)',
  diffViewerTitleBackground: 'var(--muted)',
  diffViewerTitleColor: 'var(--muted-foreground)',
  diffViewerTitleBorderColor: 'var(--border)',
};

// The library puts the label of each style in the class name, for example
// `react-diff-<hash>-diff-added`. A cell of a changed line has that class.
const onAdded = '&[class*="-diff-added"]';
const onRemoved = '&[class*="-diff-removed"]';

/** The styles prop of the diff viewers of the editor dialogs. */
export const diffViewerStyles = {
  variables: { dark: colours, light: colours },
  contentText: {
    fontFamily:
      'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
    fontSize: '12px',
    lineHeight: '1.5',
  },
  titleBlock: { padding: '4px 8px', fontSize: '12px', fontWeight: 500 },
  line: { padding: '0 8px' },
  // Line numbers do not shift when the digits change.
  lineNumber: { fontVariantNumeric: 'tabular-nums' },
  // The sign column: a 2 px bar on the left of a changed line, and the sign
  // in the colour of the change.
  marker: {
    [onAdded]: {
      boxShadow: 'inset 2px 0 0 var(--success)',
      pre: { color: sign(added) },
    },
    [onRemoved]: {
      boxShadow: 'inset 2px 0 0 var(--destructive)',
      pre: { color: sign(removed) },
    },
  },
  // A blank line that was added or removed keeps the colour of its change.
  emptyLine: {
    [onAdded]: { background: tint(added, 16) },
    [onRemoved]: { background: tint(removed, 16) },
  },
};
