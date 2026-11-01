// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';

/** Lines per chunk; the browser skips layout and paint for chunks off screen. */
export const LOG_CHUNK_LINES = 200;
/** Height a skipped chunk reserves per line until it has been measured. */
const ESTIMATED_LINE_PX = 24;

type Props = {
  count: number;
  renderLine: (index: number) => React.ReactNode;
};

/**
 * Renders log lines in fixed-size chunks with content-visibility, so a
 * 10,000-line log costs little more than the lines in view. Every line stays
 * in the DOM, so search, copy, and scrolling to a line keep working.
 */
export function LogChunks({ count, renderLine }: Props): React.ReactElement {
  const chunks: React.ReactNode[] = [];
  for (let start = 0; start < count; start += LOG_CHUNK_LINES) {
    const end = Math.min(count, start + LOG_CHUNK_LINES);
    const lines: React.ReactNode[] = [];
    for (let index = start; index < end; index++) {
      lines.push(renderLine(index));
    }
    chunks.push(
      <div
        key={start}
        data-log-chunk={start}
        style={{
          contentVisibility: 'auto',
          containIntrinsicHeight: `auto ${(end - start) * ESTIMATED_LINE_PX}px`,
        }}
      >
        {lines}
      </div>
    );
  }
  return <>{chunks}</>;
}
