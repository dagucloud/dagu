// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { DagStatusChip } from '@/components/docs-live/DagStatusChip';
import { DaguInfoBlock } from '@/components/docs-live/DaguInfoBlock';
import { DaguRunBlock } from '@/components/docs-live/DaguRunBlock';
import { useDocLive } from '@/components/docs-live/context';
import { MermaidBlock } from '@/components/ui/mermaid-block';
import { cn } from '@/lib/utils';
import { slugifyHeading } from '@/lib/text-utils';
import { useDocAttachmentUrl } from '@/hooks/useDocAttachmentUrl';
import { encodeDocPathForURL } from '@/pages/docs/lib/doc-path';
import {
  parseWikilinkHref,
  remarkWikilink,
  WIKILINK_DAG_PREFIX,
} from '@/lib/remark-wikilink';
import { isValidElement, useEffect, useState, type ReactNode } from 'react';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import { Link } from 'react-router-dom';
import remarkGfm from 'remark-gfm';
import './doc-markdown-preview.css';

// The default transform strips URLs with unrecognized protocols; wikilink:
// and attachment: URLs must survive so the overrides can resolve them.
function docUrlTransform(url: string): string {
  if (url.startsWith('wikilink:') || url.startsWith('attachment:')) return url;
  return defaultUrlTransform(url);
}

const ATTACHMENT_SCHEME = 'attachment:';

const CUSTOM_FENCE_BLOCKS = {
  'language-mermaid': {
    component: MermaidBlock,
    render: (source: string) => <MermaidBlock code={source} />,
  },
  'language-dagu-info': {
    component: DaguInfoBlock,
    render: (source: string) => <DaguInfoBlock source={source} />,
  },
  'language-dagu-run': {
    component: DaguRunBlock,
    render: (source: string) => <DaguRunBlock source={source} />,
  },
} as const;

// A bare file name with an extension (no slash, no scheme) also resolves as
// an attachment of the containing document, keeping hand-written
// ![](image.png) references portable.
function safeDecode(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    // Keep the raw value when it is not valid percent-encoding.
    return value;
  }
}

function attachmentNameFromSrc(src: string | undefined): string | null {
  if (!src) return null;
  if (src.startsWith(ATTACHMENT_SCHEME)) {
    const name = safeDecode(src.slice(ATTACHMENT_SCHEME.length));
    return name || null;
  }
  if (src.includes('/') || src.includes(':')) return null;
  return src.includes('.') ? safeDecode(src) : null;
}

/**
 * Context for resolving wikilinks in the preview. When absent (for example
 * artifact previews), wikilinks render as inert text spans.
 */
export type DocLinkContext = {
  workspace: string | null;
  docPath: string;
  /**
   * Opens a doc link in place (for example as an editor tab) instead of
   * navigating. Plain left-clicks use this when set; modified clicks keep
   * the href behavior.
   */
  onOpenDoc?: (docPath: string, workspace: string | null) => void;
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
  return `/docs/${encodeDocPathForURL(target)}${search}${hash}`;
}

type WikilinkAnchorProps = {
  href: string;
  linkContext?: DocLinkContext;
  children: ReactNode;
};

function WikilinkAnchor({ href, linkContext, children }: WikilinkAnchorProps) {
  const live = useDocLive();
  const parsed = parseWikilinkHref(href);
  if (!parsed) return <span>{children}</span>;
  if (!linkContext) {
    return <span className="wikilink wikilink-inert">{children}</span>;
  }
  if (parsed.target.startsWith(WIKILINK_DAG_PREFIX)) {
    const dagName = parsed.target.slice(WIKILINK_DAG_PREFIX.length);
    if (live) {
      const label = typeof children === 'string' ? children : dagName;
      return <DagStatusChip dagRef={dagName} label={label} />;
    }
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
  const { onOpenDoc } = linkContext;
  return (
    <Link
      to={docLinkTo(parsed.target, parsed.anchor, linkContext)}
      className="wikilink"
      data-wikilink-target={parsed.target}
      onClick={
        onOpenDoc
          ? (e) => {
              if (
                e.defaultPrevented ||
                e.button !== 0 ||
                e.metaKey ||
                e.ctrlKey ||
                e.shiftKey ||
                e.altKey
              ) {
                return;
              }
              e.preventDefault();
              onOpenDoc(parsed.target, linkContext.workspace);
            }
          : undefined
      }
    >
      {children}
    </Link>
  );
}

type DocAttachmentLinkProps = {
  name: string;
  linkContext: DocLinkContext;
  children: ReactNode;
};

// Anchor for a non-image attachment: fetches the blob on demand and triggers
// a browser download, since the attachment: scheme is not navigable.
function DocAttachmentLink({
  name,
  linkContext,
  children,
}: DocAttachmentLinkProps) {
  const [requested, setRequested] = useState(false);
  const { url, error } = useDocAttachmentUrl(
    requested ? linkContext.docPath : null,
    linkContext.workspace,
    requested ? name : null
  );

  useEffect(() => {
    if (!requested || !url) return;
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = name;
    anchor.click();
    setRequested(false);
  }, [requested, url, name]);

  return (
    <a
      href={`attachment:${encodeURIComponent(name)}`}
      className={error ? 'wikilink wikilink-inert' : 'wikilink'}
      title={error ? `Attachment not found: ${name}` : `Download ${name}`}
      onClick={(e) => {
        e.preventDefault();
        setRequested(true);
      }}
    >
      {children}
    </a>
  );
}

type DocAttachmentImageProps = {
  name: string;
  alt?: string;
  fallbackSrc?: string;
  linkContext: DocLinkContext;
};

function DocAttachmentImage({
  name,
  alt,
  fallbackSrc,
  linkContext,
}: DocAttachmentImageProps) {
  const { url, error } = useDocAttachmentUrl(
    linkContext.docPath,
    linkContext.workspace,
    name
  );
  if (error) {
    // Not a stored attachment: fall back to the authored source so ordinary
    // relative images keep their previous behavior.
    return fallbackSrc ? <img src={fallbackSrc} alt={alt ?? name} /> : null;
  }
  if (!url) return null;
  return <img src={url} alt={alt ?? name} />;
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
            if (href?.startsWith(ATTACHMENT_SCHEME)) {
              const name = safeDecode(href.slice(ATTACHMENT_SCHEME.length));
              if (!name || !linkContext) {
                return (
                  <span className="wikilink wikilink-inert">{children}</span>
                );
              }
              return (
                <DocAttachmentLink name={name} linkContext={linkContext}>
                  {children}
                </DocAttachmentLink>
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
          img({ src, alt }) {
            const source = typeof src === 'string' ? src : undefined;
            const attachmentName = attachmentNameFromSrc(source);
            if (attachmentName && linkContext) {
              return (
                <DocAttachmentImage
                  name={attachmentName}
                  alt={alt}
                  fallbackSrc={
                    source?.startsWith(ATTACHMENT_SCHEME) ? undefined : source
                  }
                  linkContext={linkContext}
                />
              );
            }
            if (source?.startsWith(ATTACHMENT_SCHEME)) return null;
            return <img src={source} alt={alt} />;
          },
          code({ className: codeClassName, children }) {
            const block = codeClassName
              ? CUSTOM_FENCE_BLOCKS[
                  codeClassName as keyof typeof CUSTOM_FENCE_BLOCKS
                ]
              : undefined;
            if (block) {
              return block.render(String(children));
            }
            return <code className={codeClassName}>{children}</code>;
          },
          pre({ children }) {
            // Fences dispatched to custom blocks must not keep the <pre>
            // wrapper. The child may be the block element itself or the code
            // override carrying the fence language in its className.
            const childArray = Array.isArray(children) ? children : [children];
            const unwrapped = childArray.some((child) => {
              if (!isValidElement(child)) return false;
              if (
                Object.values(CUSTOM_FENCE_BLOCKS).some(
                  (block) => child.type === block.component
                )
              ) {
                return true;
              }
              const className = (child.props as { className?: string })
                .className;
              return !!className && className in CUSTOM_FENCE_BLOCKS;
            });
            if (unwrapped) {
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
