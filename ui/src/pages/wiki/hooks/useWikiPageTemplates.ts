// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useClient, useQuery } from '@/hooks/api';
import { AppBarContext } from '@/contexts/AppBarContext';
import { workspaceWikiQueryForWorkspace } from '@/lib/workspace';
import { useContext, useMemo } from 'react';
import {
  BUILT_IN_WIKI_PAGE_TEMPLATES,
  WIKI_TEMPLATES_PREFIX,
  type WikiPageTemplate,
} from '../lib/wiki-page-templates';

/**
 * Templates offered in the create dialog: built-ins plus user Wiki pages under
 * `_templates/` at the root scope and in the target workspace. User template
 * content is fetched on demand via resolveTemplateContent.
 */
export function useWikiPageTemplates(
  enabled: boolean,
  workspace: string | null
) {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  const rootQuery = useMemo(() => workspaceWikiQueryForWorkspace(null), []);
  const workspaceQuery = useMemo(
    () => workspaceWikiQueryForWorkspace(workspace),
    [workspace]
  );

  const listParams = (query: Record<string, unknown>) => ({
    params: {
      query: {
        remoteNode,
        prefix: WIKI_TEMPLATES_PREFIX,
        flat: true,
        perPage: 100,
        ...query,
      },
    },
  });

  const { data: rootData } = useQuery(
    '/wiki',
    enabled ? listParams(rootQuery) : null
  );
  const { data: workspaceData } = useQuery(
    '/wiki',
    enabled && workspace ? listParams(workspaceQuery) : null
  );

  const templates = useMemo<WikiPageTemplate[]>(() => {
    const userTemplates: WikiPageTemplate[] = [];
    const seen = new Set<string>();
    const add = (items: typeof rootData, ws: string | null) => {
      items?.items?.forEach((item) => {
        const key = `${item.workspace ?? ws ?? ''}:${item.id}`;
        if (seen.has(key)) return;
        seen.add(key);
        userTemplates.push({
          id: `user:${key}`,
          name: item.title,
          description: item.description,
          content: '',
          path: item.id,
          workspace: item.workspace ?? ws ?? null,
          builtIn: false,
        });
      });
    };
    add(rootData, null);
    add(workspaceData, workspace);
    return [...BUILT_IN_WIKI_PAGE_TEMPLATES, ...userTemplates];
  }, [rootData, workspaceData, workspace]);

  return templates;
}

/** Fetch the content backing a template; built-ins resolve locally. */
export function useResolveTemplateContent() {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  return async (template: WikiPageTemplate): Promise<string> => {
    if (template.builtIn || !template.path) return template.content;
    const { data, error } = await client.GET('/wiki/page', {
      params: {
        query: {
          remoteNode,
          path: template.path,
          ...workspaceWikiQueryForWorkspace(template.workspace ?? null),
        },
      },
    });
    if (error || !data) {
      throw new Error(error?.message || 'Failed to load template');
    }
    return data.content;
  };
}
