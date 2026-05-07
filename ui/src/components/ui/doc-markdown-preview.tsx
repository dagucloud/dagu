// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { MermaidBlock } from '@/components/ui/mermaid-block';
import { cn } from '@/lib/utils';
import { slugifyHeading } from '@/lib/text-utils';
import type { ReactElement, ReactNode } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import './doc-markdown-preview.css';

type DocMarkdownPreviewProps = {
  content: string | null | undefined;
  className?: string;
};

function headingId(children: ReactNode): string {
  const text =
    typeof children === 'string'
      ? children
      : Array.isArray(children)
        ? children
            .map((child) => (typeof child === 'string' ? child : ''))
            .join('')
        : String(children ?? '');
  return slugifyHeading(text);
}

function stripFrontmatter(content: string): string {
  return content.replace(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/, '');
}

export function DocMarkdownPreview({
  content,
  className,
}: DocMarkdownPreviewProps) {
  return (
    <div className={cn('doc-preview max-w-none', className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: ({ children }) => <h1 id={headingId(children)}>{children}</h1>,
          h2: ({ children }) => <h2 id={headingId(children)}>{children}</h2>,
          h3: ({ children }) => <h3 id={headingId(children)}>{children}</h3>,
          h4: ({ children }) => <h4 id={headingId(children)}>{children}</h4>,
          h5: ({ children }) => <h5 id={headingId(children)}>{children}</h5>,
          h6: ({ children }) => <h6 id={headingId(children)}>{children}</h6>,
          code({ className: codeClassName, children }) {
            if (codeClassName === 'language-mermaid') {
              return <MermaidBlock code={String(children)} />;
            }
            return <code className={codeClassName}>{children}</code>;
          },
          pre({ children }) {
            const child = children as ReactElement;
            if (child?.type === MermaidBlock) {
              return <>{children}</>;
            }
            return <pre>{children}</pre>;
          },
        }}
      >
        {stripFrontmatter(content ?? '')}
      </ReactMarkdown>
    </div>
  );
}
