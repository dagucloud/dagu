// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { MermaidBlock } from '@/components/ui/mermaid-block';
import { cn } from '@/lib/utils';
import { slugifyHeading } from '@/lib/text-utils';
import {
  parseWikilinkHref,
  remarkWikilink,
  WIKILINK_DAG_PREFIX,
} from '@/lib/remark-wikilink';
import type { ReactElement, ReactNode } from 'react';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import { Link } from 'react-router-dom';
import remarkGfm from 'remark-gfm';
import './doc-markdown-preview.css';

// The default transform strips URLs with unrecognized protocols; wikilink:
// URLs must survive so the anchor override can resolve them.
function docUrlTransform(url: string): string {
  if (url.startsWith('wikilink:')) return url;
  return defaultUrlTransform(url);
}

/**
 * Context for resolving wikilinks in the preview. When absent (for example
 * artifact previews), wikilinks render as inert text spans.
 */
export type DocLinkContext = {
  workspace: string | null;
  docPath: string;
};

type DocMarkdownPreviewProps = {
  content: string | null | undefined;
  className?: string;
  linkContext?: DocLinkContext;
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

function docLinkTo(target: string, anchor: string, context: DocLinkContext) {
  const search = context.workspace
    ? `?workspace=${encodeURIComponent(context.workspace)}`
    : '';
  const hash = anchor ? `#${slugifyHeading(anchor)}` : '';
  return `/docs/${encodeURI(target)}${search}${hash}`;
}

type WikilinkAnchorProps = {
  href: string;
  linkContext?: DocLinkContext;
  children: ReactNode;
};

function WikilinkAnchor({ href, linkContext, children }: WikilinkAnchorProps) {
  const parsed = parseWikilinkHref(href);
  if (!parsed) return <span>{children}</span>;
  if (!linkContext) {
    return <span className="wikilink wikilink-inert">{children}</span>;
  }
  if (parsed.target.startsWith(WIKILINK_DAG_PREFIX)) {
    const dagName = parsed.target.slice(WIKILINK_DAG_PREFIX.length);
    return (
      <Link
        to={`/dags/${encodeURIComponent(dagName)}`}
        className="wikilink wikilink-dag"
        data-wikilink-target={parsed.target}
      >
        {children}
      </Link>
    );
  }
  return (
    <Link
      to={docLinkTo(parsed.target, parsed.anchor, linkContext)}
      className="wikilink"
      data-wikilink-target={parsed.target}
    >
      {children}
    </Link>
  );
}

export function DocMarkdownPreview({
  content,
  className,
  linkContext,
}: DocMarkdownPreviewProps) {
  return (
    <div className={cn('doc-preview max-w-none', className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkWikilink]}
        urlTransform={docUrlTransform}
        components={{
          h1: ({ children }) => <h1 id={headingId(children)}>{children}</h1>,
          h2: ({ children }) => <h2 id={headingId(children)}>{children}</h2>,
          h3: ({ children }) => <h3 id={headingId(children)}>{children}</h3>,
          h4: ({ children }) => <h4 id={headingId(children)}>{children}</h4>,
          h5: ({ children }) => <h5 id={headingId(children)}>{children}</h5>,
          h6: ({ children }) => <h6 id={headingId(children)}>{children}</h6>,
          a({ href, children }) {
            if (href?.startsWith('wikilink:')) {
              return (
                <WikilinkAnchor href={href} linkContext={linkContext}>
                  {children}
                </WikilinkAnchor>
              );
            }
            if (href?.startsWith('http://') || href?.startsWith('https://')) {
              return (
                <a href={href} target="_blank" rel="noreferrer noopener">
                  {children}
                </a>
              );
            }
            return <a href={href}>{children}</a>;
          },
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
