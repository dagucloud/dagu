// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useClient } from '@/hooks/api';
import { AppBarContext } from '@/contexts/AppBarContext';
import { workspaceWikiQueryForWorkspace } from '@/lib/workspace';
import { useContext, useEffect, useState } from 'react';

// Blob MIME types by extension. Unknown extensions download as opaque data;
// SVG served through a blob URL in an <img> cannot execute scripts.
const ATTACHMENT_MIME_TYPES: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  svg: 'image/svg+xml',
  avif: 'image/avif',
  bmp: 'image/bmp',
};

export function attachmentMimeType(name: string): string {
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  return ATTACHMENT_MIME_TYPES[ext] ?? 'application/octet-stream';
}

/**
 * Fetch a page attachment and expose it as a typed blob object URL. The URL is
 * revoked when inputs change or the consumer unmounts.
 */
export function useWikiPageAttachmentUrl(
  wikiPagePath: string | null,
  workspace: string | null,
  name: string | null
): { url: string | null; error: boolean } {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const [url, setUrl] = useState<string | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    if (!wikiPagePath || !name) {
      setUrl(null);
      setError(false);
      return;
    }
    let objectUrl: string | null = null;
    let cancelled = false;
    setUrl(null);
    setError(false);

    const load = async () => {
      const { data, error: fetchError } = await client.GET(
        '/wiki/page/attachment',
        {
          params: {
            query: {
              remoteNode,
              path: wikiPagePath,
              name,
              ...workspaceWikiQueryForWorkspace(workspace),
            },
          },
          parseAs: 'blob',
        }
      );
      if (cancelled) return;
      if (fetchError || !data) {
        setError(true);
        return;
      }
      const typed = new Blob([data], { type: attachmentMimeType(name) });
      objectUrl = URL.createObjectURL(typed);
      setUrl(objectUrl);
    };

    void load().catch(() => {
      if (!cancelled) setError(true);
    });

    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [client, remoteNode, wikiPagePath, workspace, name]);

  return { url, error };
}
