// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useClient, useQuery } from '@/hooks/api';
import { AppBarContext } from '@/contexts/AppBarContext';
import { workspaceDocumentQueryForWorkspace } from '@/lib/workspace';
import { useContext, useMemo } from 'react';
import {
  BUILT_IN_DOC_TEMPLATES,
  DOC_TEMPLATES_PREFIX,
  type DocTemplate,
} from '../lib/doc-templates';

/**
 * Templates offered in the create dialog: built-ins plus user documents under
 * `_templates/` at the root scope and in the target workspace. User template
 * content is fetched on demand via resolveTemplateContent.
 */
export function useDocTemplates(enabled: boolean, workspace: string | null) {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  const rootQuery = useMemo(
    () => workspaceDocumentQueryForWorkspace(null),
    []
  );
  const workspaceQuery = useMemo(
    () => workspaceDocumentQueryForWorkspace(workspace),
    [workspace]
  );

  const listParams = (query: Record<string, unknown>) => ({
    params: {
      query: {
        remoteNode,
        prefix: DOC_TEMPLATES_PREFIX,
        flat: true,
        perPage: 100,
        ...query,
      },
    },
  });

  const { data: rootData } = useQuery(
    '/docs',
    enabled ? listParams(rootQuery) : null
  );
  const { data: workspaceData } = useQuery(
    '/docs',
    enabled && workspace ? listParams(workspaceQuery) : null
  );

  const templates = useMemo<DocTemplate[]>(() => {
    const userTemplates: DocTemplate[] = [];
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
    return [...BUILT_IN_DOC_TEMPLATES, ...userTemplates];
  }, [rootData, workspaceData, workspace]);

  return templates;
}

/** Fetch the content backing a template; built-ins resolve locally. */
export function useResolveTemplateContent() {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  return async (template: DocTemplate): Promise<string> => {
    if (template.builtIn || !template.path) return template.content;
    const { data, error } = await client.GET('/docs/doc', {
      params: {
        query: {
          remoteNode,
          path: template.path,
          ...workspaceDocumentQueryForWorkspace(template.workspace ?? null),
        },
      },
    });
    if (error || !data) {
      throw new Error(error?.message || 'Failed to load template');
    }
    return data.content;
  };
}
