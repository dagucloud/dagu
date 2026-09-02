// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { BookOpen, FilePlus, Link2 } from 'lucide-react';
import React, { useContext, useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  components,
  PathsWikiGetParametersQueryOrder,
  PathsWikiGetParametersQuerySort,
} from '@/api/v1/schema';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useClient, useQuery } from '@/hooks/api';
import { workspaceWikiQueryForWorkspace } from '@/lib/workspace';
import { CreateWikiPageModal } from '@/pages/wiki/components/CreateWikiPageModal';
import { encodeWikiPagePathForURL } from '@/pages/wiki/lib/wiki-page-path';
import {
  BUILT_IN_WIKI_PAGE_TEMPLATES,
  WIKI_PAGE_TEMPLATE_DAG_NAME,
} from '@/pages/wiki/lib/wiki-page-templates';
import { I18nText } from '@/i18n/I18nText';
import { I18nTemplate } from '@/i18n/I18nTemplate';

type WikiPageMetadataResponse =
  components['schemas']['WikiPageMetadataResponse'];

type Props = {
  dagName: string;
  workspaceName?: string;
};

// Wiki page IDs cannot contain every character a DAG name can; hide the folder
// convention when the name would not form a valid page path segment.
function isValidWikiPageSegment(name: string): boolean {
  return /^[a-zA-Z0-9_][a-zA-Z0-9_. -]*$/.test(name) && !/[. ]$/.test(name);
}

function wikiPageLink(
  item: WikiPageMetadataResponse,
  fallbackWorkspace: string | null
) {
  const workspace = item.workspace ?? fallbackWorkspace;
  const search = workspace ? `?workspace=${encodeURIComponent(workspace)}` : '';
  return `/wiki/${encodeWikiPagePathForURL(item.id)}${search}`;
}

function WikiPageRow({
  item,
  workspace,
}: {
  item: WikiPageMetadataResponse;
  workspace: string | null;
}) {
  return (
    <Link
      to={wikiPageLink(item, workspace)}
      className="block px-3 py-1.5 hover:bg-accent border-b border-border last:border-b-0"
    >
      <div className="min-w-0 whitespace-normal break-words text-xs font-medium">
        {item.title}
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-2 text-[10px] text-muted-foreground">
        <span className="min-w-0 whitespace-normal break-words">{item.id}</span>
        {(item.tags ?? []).map((tag) => (
          <span
            key={tag}
            className="max-w-full whitespace-normal break-words px-1 rounded-full bg-muted border border-border"
          >
            {tag}
          </span>
        ))}
      </div>
    </Link>
  );
}

function DAGWikiTab({ dagName, workspaceName }: Props) {
  const config = useConfig();
  const client = useClient();
  const navigate = useNavigate();
  const { showToast } = useSimpleToast();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspace = workspaceName ?? null;
  const canWriteWorkspace = useCanWriteForWorkspace(workspace);
  const canWrite = config.permissions.writeDags && canWriteWorkspace;
  const workspaceQuery = useMemo(
    () => workspaceWikiQueryForWorkspace(workspace),
    [workspace]
  );

  const validSegment = isValidWikiPageSegment(dagName);
  const [createOpen, setCreateOpen] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [createLoading, setCreateLoading] = useState(false);

  // Convention folder under {workspace}/{dagName}/. Running steps receive the
  // same location as DAG_WIKI_DIR and the deprecated DAG_DOCS_DIR alias.
  const { data: folderData, mutate: mutateFolder } = useQuery(
    '/wiki',
    validSegment && dagName
      ? {
          params: {
            query: {
              remoteNode,
              prefix: dagName,
              flat: true,
              sort: PathsWikiGetParametersQuerySort.mtime,
              order: PathsWikiGetParametersQueryOrder.desc,
              perPage: 50,
              ...workspaceQuery,
            },
          },
        }
      : null
  );
  const folderWikiPages = folderData?.items ?? [];

  // Wiki pages referencing this DAG via [[dag:name]] wikilinks.
  const { data: backlinkData } = useQuery(
    '/wiki/backlinks',
    dagName
      ? {
          params: {
            query: {
              remoteNode,
              target: `dag:${dagName}`,
              ...workspaceQuery,
            },
          },
        }
      : null
  );
  const folderIds = useMemo(
    () => new Set(folderWikiPages.map((d) => d.id)),
    [folderWikiPages]
  );
  const backlinkWikiPages = (backlinkData?.items ?? []).filter(
    (d) => !folderIds.has(d.id)
  );

  const runbookTemplate = useMemo(() => {
    const template = BUILT_IN_WIKI_PAGE_TEMPLATES.find(
      (t) => t.name === 'Runbook'
    );
    return (template?.content ?? '')
      .split(WIKI_PAGE_TEMPLATE_DAG_NAME)
      .join(dagName);
  }, [dagName]);

  const handleCreate = async (path: string, content: string) => {
    setCreateLoading(true);
    setCreateError(null);
    try {
      // The button promises a runbook: a blank selection gets the runbook
      // template, and any chosen template gets the DAG name substituted.
      const body =
        content === ''
          ? runbookTemplate
          : content.split(WIKI_PAGE_TEMPLATE_DAG_NAME).join(dagName);
      const { error } = await client.POST('/wiki', {
        params: { query: { remoteNode, ...workspaceQuery } },
        body: { id: path, content: body },
      });
      if (error) {
        setCreateError(error.message || 'Failed to create Wiki page');
        return;
      }
      setCreateOpen(false);
      showToast('Runbook created');
      mutateFolder();
      const search = workspace
        ? `?workspace=${encodeURIComponent(workspace)}`
        : '';
      navigate(`/wiki/${encodeWikiPagePathForURL(path)}${search}`);
    } catch (error) {
      setCreateError(
        error instanceof Error ? error.message : 'Failed to create Wiki page'
      );
    } finally {
      setCreateLoading(false);
    }
  };

  const empty = folderWikiPages.length === 0 && backlinkWikiPages.length === 0;

  return (
    <div className="rounded-md border border-border bg-background">
      <div className="flex items-center gap-2 px-3 py-2 border-b border-border">
        <BookOpen className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium">
          <I18nText text={'Wiki'} />
        </span>
        <div className="flex-1" />
        {canWrite && validSegment && (
          <button
            type="button"
            onClick={() => setCreateOpen(true)}
            className="flex items-center gap-1 px-2 py-0.5 text-xs rounded-md border border-border text-muted-foreground hover:text-foreground hover:bg-muted"
          >
            <FilePlus className="h-3.5 w-3.5" />
            <I18nText text={'New runbook'} />
          </button>
        )}
      </div>

      {empty ? (
        <div className="px-3 py-6 text-center text-xs text-muted-foreground space-y-1">
          <p>
            <I18nText text={'No Wiki pages reference this DAG yet.'} />
          </p>
          <p>
            <I18nTemplate
              text="Wiki pages under {folder} or containing a {link} wikilink appear here."
              values={{
                folder: (
                  <code>
                    {validSegment ? (
                      `${dagName}/`
                    ) : (
                      <I18nText text="its folder" />
                    )}
                  </code>
                ),
                link: <code>{`[[dag:${dagName}]]`}</code>,
              }}
            />
          </p>
        </div>
      ) : (
        <>
          {folderWikiPages.length > 0 && (
            <div>
              <div className="px-3 pt-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                <I18nText text={'In'} /> {dagName}/
              </div>
              {folderWikiPages.map((item) => (
                <WikiPageRow
                  key={`${item.workspace ?? ''}/${item.id}`}
                  item={item}
                  workspace={workspace}
                />
              ))}
            </div>
          )}
          {backlinkWikiPages.length > 0 && (
            <div>
              <div className="flex items-center gap-1 px-3 pt-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                <Link2 className="h-3 w-3" />
                <I18nText text={'Linking to this DAG'} />
              </div>
              {backlinkWikiPages.map((item) => (
                <WikiPageRow
                  key={`bl-${item.workspace ?? ''}/${item.id}`}
                  item={item}
                  workspace={workspace}
                />
              ))}
            </div>
          )}
        </>
      )}

      <CreateWikiPageModal
        isOpen={createOpen}
        onClose={() => setCreateOpen(false)}
        onSubmit={handleCreate}
        parentDir={validSegment ? dagName : ''}
        workspace={workspace}
        isLoading={createLoading}
        externalError={createError}
      />
    </div>
  );
}

export default DAGWikiTab;
