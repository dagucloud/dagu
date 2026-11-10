// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Fragment, useMemo } from 'react';
import { cn } from '@/lib/utils';

// The metrics of the YAML editor (editorOptions): a 13px monospace font on
// 20px lines under an 8px padding, a 65px gutter whose numbers take 39px,
// and a 16px row for each "Open step" lens.
const GUTTER_PX = 65;
const NUMBERS_PX = 39;

/** Lines past this many are below the fold of any editor. */
const MAX_LINES = 200;

/**
 * Bars in place of the text of a YAML editor that is still loading: one for
 * each line, at its indent and length, and a short one in the row of each
 * "Open step" lens. The text then shows where its bars were.
 */
export function EditorSkeleton({
  text,
  lenses,
  className,
}: {
  text: string;
  /** The steps with an "Open step" lens above their line, from 1. */
  lenses?: readonly { line: number }[];
  className?: string;
}) {
  const lines = text.split('\n').slice(0, MAX_LINES);
  const lensLines = useMemo(
    () => new Set(lenses?.map((lens) => lens.line)),
    [lenses]
  );
  return (
    <div
      aria-hidden="true"
      data-testid="editor-skeleton"
      className={cn(
        'pointer-events-none absolute inset-0 overflow-hidden pt-2 font-mono text-[13px] motion-safe:animate-pulse',
        className
      )}
    >
      {lines.map((line, index) => {
        const indent = line.length - line.trimStart().length;
        const length = line.trim().length;
        return (
          <Fragment key={index}>
            {lensLines.has(index + 1) && (
              <div className="flex h-4 items-center">
                <span
                  className="h-1.5 w-[7ch] rounded-sm bg-muted"
                  style={{ marginLeft: `calc(${GUTTER_PX}px + ${indent}ch)` }}
                />
              </div>
            )}
            <div className="flex h-5 items-center">
              <span
                className="flex shrink-0 justify-end"
                style={{
                  width: NUMBERS_PX,
                  marginRight: GUTTER_PX - NUMBERS_PX,
                }}
              >
                <span
                  className="h-2 rounded-sm bg-muted"
                  style={{ width: `${String(index + 1).length}ch` }}
                />
              </span>
              {length > 0 && (
                <span
                  className="h-2.5 rounded-sm bg-muted"
                  style={{ marginLeft: `${indent}ch`, width: `${length}ch` }}
                />
              )}
            </div>
          </Fragment>
        );
      })}
    </div>
  );
}
