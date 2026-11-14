// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Fragment, useMemo, type ReactElement } from 'react';
import { SYNTAX_CLASS } from '@/lib/syntax-colors';
import { cn } from '@/lib/utils';
import { tokenizeYaml } from './yamlTokenizer';

export interface YamlSnippetProps {
  /** The YAML text. The pre shows exactly this text. */
  source: string;
  /**
   * Keys that the step must have. A line that defines one of them gets a
   * star after its text and the attribute `data-required`.
   */
  required?: readonly string[];
  /** The tooltip of the star of a required line. */
  requiredLabel?: string;
  className?: string;
  'data-testid'?: string;
}

/**
 * A short YAML text with the colours of the code editor (light and dark):
 * keys, strings, numbers, true/false/null, comments and variable references.
 * The text content of the element equals `source`: the colours are classes
 * and the star of a required line comes from CSS, so copy and search work.
 */
export function YamlSnippet({
  source,
  required = [],
  requiredLabel,
  className,
  'data-testid': testId,
}: YamlSnippetProps): ReactElement {
  const lines = useMemo(() => tokenizeYaml(source), [source]);
  const requiredSet = useMemo(() => new Set(required), [required]);
  return (
    <pre
      data-testid={testId}
      className={cn('overflow-auto font-mono text-[11px]', className)}
    >
      {lines.map((line, index) => {
        const isRequired = line.key !== undefined && requiredSet.has(line.key);
        return (
          <Fragment key={index}>
            <span
              data-required={isRequired ? 'true' : undefined}
              title={isRequired ? requiredLabel : undefined}
              className={
                isRequired
                  ? "after:ml-1.5 after:font-semibold after:text-destructive after:content-['*']"
                  : undefined
              }
            >
              {line.tokens.map((token, i) => (
                <span key={i} className={SYNTAX_CLASS[token.kind]}>
                  {token.text}
                </span>
              ))}
            </span>
            {index < lines.length - 1 ? '\n' : null}
          </Fragment>
        );
      })}
    </pre>
  );
}
