// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ExternalLink } from 'lucide-react';
import { useContext, type ReactElement, type ReactNode } from 'react';
import { ConfigContext } from '@/contexts/ConfigContext';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { PageLink } from './targets';

/** The URL of a route of the app: the base path of the app, then the route. */
export function useAppHref(): (path: string) => string {
  const config = useContext(ConfigContext);
  const base = config?.basePath ?? '';
  return (path) => `${base}${path}`;
}

const ICON_CLASS =
  'nodrag nopan inline-flex size-4 shrink-0 items-center justify-center rounded text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring';

/**
 * A small icon that opens the page of an entry in a new tab, so that the
 * draft of the step stays. Nothing is drawn without a page. The icon is
 * always in the tab order: `className` decides when it shows.
 */
export function OpenIcon({
  link,
  name,
  className,
}: {
  link: PageLink | null;
  /** The name for the accessible label: "Open <name>". */
  name: string;
  className?: string | undefined;
}): ReactElement | null {
  const { ts } = useI18n();
  const href = useAppHref();
  if (!link) return null;
  const stop = (event: { stopPropagation(): void }) => event.stopPropagation();
  return (
    <a
      data-testid="ve-open-link"
      href={href(link.path)}
      target="_blank"
      rel="noopener"
      aria-label={ts('Open {name}', { name })}
      title={ts('Opens in a new tab')}
      className={cn(ICON_CLASS, className)}
      onClick={stop}
      onDoubleClick={stop}
      onKeyDown={stop}
    >
      <ExternalLink className="size-3" aria-hidden="true" />
    </a>
  );
}

/** A text link to a page, for the forms: it opens a new tab. */
export function OpenTextLink({
  page,
  children,
  className,
}: {
  page: PageLink;
  children: ReactNode;
  className?: string;
}): ReactElement {
  const { ts } = useI18n();
  const href = useAppHref();
  return (
    <a
      data-testid="ve-open-text-link"
      href={href(page.path)}
      target="_blank"
      rel="noopener"
      title={ts('Opens in a new tab')}
      className={cn(
        'inline-flex items-center gap-1 rounded text-xs text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
        className
      )}
    >
      <ExternalLink className="size-3 shrink-0" aria-hidden="true" />
      {children}
    </a>
  );
}
