// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  PathsDocsGetParametersQueryOrder,
  PathsDocsGetParametersQuerySort,
} from '@/api/v1/schema';
import { useConfig } from '@/contexts/ConfigContext';
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient, useQuery } from '@/hooks/api';
import { useSimpleToast } from '@/components/ui/simple-toast';
import {
  BUILT_IN_DOC_TEMPLATES,
  DOC_TEMPLATE_DAG_NAME,
} from '@/pages/docs/lib/doc-templates';
import { workspaceDocumentQueryForWorkspace } from '@/lib/workspace';
import { BookOpen, FilePlus, Link2 } from 'lucide-react';
import React, { useContext, useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { CreateDocModal } from '@/pages/docs/components/CreateDocModal';
import { encodeDocPathForURL } from '@/pages/docs/lib/doc-path';

type DocMetadataResponse = components['schemas']['DocMetadataResponse'];

type Props = {
  dagName: string;
  workspaceName?: string;
};

// Doc IDs cannot contain every character a DAG name can; hide the folder
// convention when the name would not form a valid doc path segment.
function isValidDocSegment(name: string): boolean {
  return /^[a-zA-Z0-9_][a-zA-Z0-9_. -]*$/.test(name) && !/[. ]$/.test(name);
}

function docLink(item: DocMetadataResponse, fallbackWorkspace: string | null) {
  const workspace = item.workspace ?? fallbackWorkspace;
  const search = workspace ? `?workspace=${encodeURIComponent(workspace)}` : '';
  return `/docs/${encodeDocPathForURL(item.id)}${search}`;
}

function DocRow({
  item,
  workspace,
}: {
  item: DocMetadataResponse;
  workspace: string | null;
}) {
  return (
    <Link
      to={docLink(item, workspace)}
      className="block px-3 py-1.5 hover:bg-accent border-b border-border last:border-b-0"
    >
      <div className="text-xs font-medium">{item.title}</div>
      <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
        <span className="truncate">{item.id}</span>
        {(item.tags ?? []).map((tag) => (
          <span
            key={tag}
            className="px-1 rounded-full bg-muted border border-border shrink-0"
          >
            {tag}
          </span>
        ))}
      </div>
    </Link>
  );
}

function DAGDocsTab({ dagName, workspaceName }: Props) {
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
    () => workspaceDocumentQueryForWorkspace(workspace),
    [workspace]
  );

  const validSegment = isValidDocSegment(dagName);
  const [createOpen, setCreateOpen] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [createLoading, setCreateLoading] = useState(false);

  // Convention folder: docs under {workspace}/{dagName}/ (the same location
  // running steps see as DAG_DOCS_DIR).
  const { data: folderData, mutate: mutateFolder } = useQuery(
    '/docs',
    validSegment && dagName
      ? {
          params: {
            query: {
              remoteNode,
              prefix: dagName,
              flat: true,
              sort: PathsDocsGetParametersQuerySort.mtime,
              order: PathsDocsGetParametersQueryOrder.desc,
              perPage: 50,
              ...workspaceQuery,
            },
          },
        }
      : null
  );
  const folderDocs = folderData?.items ?? [];

  // Documents referencing this DAG via [[dag:name]] wikilinks.
  const { data: backlinkData } = useQuery(
    '/docs/backlinks',
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
    () => new Set(folderDocs.map((d) => d.id)),
    [folderDocs]
  );
  const backlinkDocs = (backlinkData?.items ?? []).filter(
    (d) => !folderIds.has(d.id)
  );

  const runbookTemplate = useMemo(() => {
    const template = BUILT_IN_DOC_TEMPLATES.find((t) => t.name === 'Runbook');
    return (template?.content ?? '').split(DOC_TEMPLATE_DAG_NAME).join(dagName);
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
          : content.split(DOC_TEMPLATE_DAG_NAME).join(dagName);
      const { error } = await client.POST('/docs', {
        params: { query: { remoteNode, ...workspaceQuery } },
        body: { id: path, content: body },
      });
      if (error) {
        setCreateError(error.message || 'Failed to create document');
        return;
      }
      setCreateOpen(false);
      showToast('Runbook created');
      mutateFolder();
      const search = workspace
        ? `?workspace=${encodeURIComponent(workspace)}`
        : '';
      navigate(`/docs/${encodeDocPathForURL(path)}${search}`);
    } catch (error) {
      setCreateError(
        error instanceof Error ? error.message : 'Failed to create document'
      );
    } finally {
      setCreateLoading(false);
    }
  };

  const empty = folderDocs.length === 0 && backlinkDocs.length === 0;

  return (
    <div className="rounded-md border border-border bg-background">
      <div className="flex items-center gap-2 px-3 py-2 border-b border-border">
        <BookOpen className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium">Docs</span>
        <div className="flex-1" />
        {canWrite && validSegment && (
          <button
            type="button"
            onClick={() => setCreateOpen(true)}
            className="flex items-center gap-1 px-2 py-0.5 text-xs rounded-md border border-border text-muted-foreground hover:text-foreground hover:bg-muted"
          >
            <FilePlus className="h-3.5 w-3.5" />
            New runbook
          </button>
        )}
      </div>

      {empty ? (
        <div className="px-3 py-6 text-center text-xs text-muted-foreground space-y-1">
          <p>No documents reference this DAG yet.</p>
          <p>
            Documents under{' '}
            <code>{validSegment ? `${dagName}/` : 'its folder'}</code> or
            containing a <code>{`[[dag:${dagName}]]`}</code> wikilink appear
            here.
          </p>
        </div>
      ) : (
        <>
          {folderDocs.length > 0 && (
            <div>
              <div className="px-3 pt-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                In {dagName}/
              </div>
              {folderDocs.map((item) => (
                <DocRow
                  key={`${item.workspace ?? ''}/${item.id}`}
                  item={item}
                  workspace={workspace}
                />
              ))}
            </div>
          )}
          {backlinkDocs.length > 0 && (
            <div>
              <div className="flex items-center gap-1 px-3 pt-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                <Link2 className="h-3 w-3" />
                Linking to this DAG
              </div>
              {backlinkDocs.map((item) => (
                <DocRow
                  key={`bl-${item.workspace ?? ''}/${item.id}`}
                  item={item}
                  workspace={workspace}
                />
              ))}
            </div>
          )}
        </>
      )}

      <CreateDocModal
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

export default DAGDocsTab;
