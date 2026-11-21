// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ImageOff } from 'lucide-react';
import type { ReactElement } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import '@/components/ui/wiki-page-markdown-preview.css';
import { cn } from '@/lib/utils';
import { linkedSkillPath } from './skillFiles';

const WEB_LINK = /^(?:https?|mailto):/i;

/**
 * The preview of a Markdown file of a skill. A skill can come from anyone, so
 * the preview loads nothing and runs nothing: an image shows its text, a
 * fenced block stays code, and only a web address opens, in a new tab. A
 * relative link to a file of the skill opens that file in the editor; any
 * other link is plain text.
 */
export function SkillPreview({
  content,
  path,
  files,
  onOpenFile,
  className,
}: {
  content: string;
  /** The path of the file in the skill. Relative links start from its folder. */
  path: string;
  /** The paths of the files of the skill. */
  files: readonly string[];
  onOpenFile(path: string): void;
  className?: string;
}): ReactElement {
  return (
    <div className={cn('wiki-page-preview max-w-none', className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          a({ href, children }) {
            if (href && WEB_LINK.test(href)) {
              return (
                <a href={href} target="_blank" rel="noreferrer noopener">
                  {children}
                </a>
              );
            }
            const target = href ? linkedSkillPath(path, href) : null;
            if (target && files.includes(target)) {
              return (
                <button
                  type="button"
                  title={target}
                  className="text-primary underline underline-offset-2"
                  onClick={() => onOpenFile(target)}
                >
                  {children}
                </button>
              );
            }
            return <span title={href}>{children}</span>;
          },
          img({ src, alt }) {
            const source = typeof src === 'string' ? src : '';
            return (
              <span
                title={source}
                className="inline-flex items-center gap-1 rounded border border-border px-1 text-xs text-muted-foreground"
              >
                <ImageOff className="size-3 shrink-0" aria-hidden="true" />
                {alt || source}
              </span>
            );
          },
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
