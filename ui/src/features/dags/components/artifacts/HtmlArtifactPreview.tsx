// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useMemo } from 'react';
import { cn } from '@/lib/utils';
import { buildHTMLArtifactPreviewDocument } from './htmlPreview';
import { I18nProps } from '@/i18n/I18nProps';

type Props = {
  content?: string;
  fillHeight?: boolean;
  className?: string;
};

export function HtmlArtifactPreview({
  content = '',
  fillHeight = false,
  className,
}: Props) {
  const srcDoc = useMemo(
    () => buildHTMLArtifactPreviewDocument(content),
    [content]
  );

  return (
    <I18nProps><iframe
      title="HTML artifact preview"
      sandbox=""
      referrerPolicy="no-referrer"
      srcDoc={srcDoc}
      className={cn(
        'w-full rounded-md border border-border bg-white',
        fillHeight ? 'h-full min-h-[24rem]' : 'h-[32rem]',
        className
      )}
    /></I18nProps>
  );
}
