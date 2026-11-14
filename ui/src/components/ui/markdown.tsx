import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { CSSProperties } from 'react';
import { cn } from '@/lib/utils';
import { MermaidBlock } from '@/components/ui/mermaid-block';

interface MarkdownProps {
  content: string;
  className?: string;
}

const inlineCodeStyle: CSSProperties = {
  backgroundColor: 'var(--muted)',
  color: 'var(--foreground)',
};

const codeBlockStyle: CSSProperties = {
  backgroundColor: 'var(--muted)',
  color: 'var(--foreground)',
};

const codeBlockInnerStyle: CSSProperties = {
  backgroundColor: 'transparent',
  color: 'var(--foreground)',
};

// The base styles size headings for pages and drop list markers, so the
// rendered elements are restyled here to read at the size of the text.
const elementStyles = [
  '[&_p]:my-1 [&_p]:leading-relaxed',
  '[&_pre]:my-1',
  '[&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-4',
  '[&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-4',
  '[&_li]:my-0.5',
  '[&_:is(h1,h2,h3,h4,h5,h6)]:mt-2 [&_:is(h1,h2,h3,h4,h5,h6)]:mb-1 [&_:is(h1,h2,h3,h4,h5,h6)]:font-semibold',
  '[&_h1]:text-[1.15em] [&_h2]:text-[1.1em] [&_:is(h3,h4,h5,h6)]:text-[1em]',
  '[&_blockquote]:my-1 [&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-2 [&_blockquote]:text-muted-foreground',
  '[&_table]:my-1 [&_table]:text-xs [&_:is(th,td)]:border [&_:is(th,td)]:border-border [&_:is(th,td)]:px-1.5 [&_:is(th,td)]:py-0.5',
  '[&_a]:text-primary [&_a]:underline [&_a]:underline-offset-2',
  '[&>:first-child]:mt-0 [&>:last-child]:mb-0',
];

// Compact markdown renderer for chat messages
export function Markdown({ content, className }: MarkdownProps) {
  return (
    <div className={cn('text-xs break-words', elementStyles, className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          // Custom code block rendering
          code({ className: codeClassName, children, style, ...props }) {
            const isInline = !codeClassName;
            if (isInline) {
              return (
                <code
                  className="text-xs bg-muted text-foreground px-1 py-0.5 rounded font-mono"
                  style={{ ...inlineCodeStyle, ...style }}
                  {...props}
                >
                  {children}
                </code>
              );
            }
            // Mermaid code blocks
            if (codeClassName === 'language-mermaid') {
              return <MermaidBlock code={String(children)} />;
            }
            // Block code
            return (
              <code
                className={cn(
                  'block text-xs font-mono text-foreground',
                  codeClassName
                )}
                style={{ ...codeBlockInnerStyle, ...style }}
                {...props}
              >
                {children}
              </code>
            );
          },
          // Ensure pre blocks have proper styling
          pre({ children, style, ...props }) {
            // Pass through mermaid blocks without wrapping in pre
            const child = children as React.ReactElement;
            if (child?.type === MermaidBlock) {
              return <>{children}</>;
            }
            return (
              <pre
                className="text-xs p-2 rounded border border-border bg-muted text-foreground overflow-x-auto font-mono"
                style={{ ...codeBlockStyle, ...style }}
                {...props}
              >
                {children}
              </pre>
            );
          },
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
