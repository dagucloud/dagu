// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { cn } from '@/lib/utils';
import {
  AlertCircle,
  File,
  FileCode,
  FileImage,
  FileText,
  Folder,
  FolderOpen,
  RefreshCw,
} from 'lucide-react';
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { components } from '../../../../api/v1/schema';
import { ArtifactFilePreview } from './ArtifactFilePreview';
import { useArtifactTreeNavigation } from './useArtifactTreeNavigation';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { useI18n } from '@/i18n/I18nProvider';

type ArtifactTreeNode = components['schemas']['ArtifactTreeNode'];
type DAGRunDetails = components['schemas']['DAGRunDetails'];

type Props = {
  dagRun: DAGRunDetails;
  artifactEnabled?: boolean;
  className?: string;
  fillHeight?: boolean;
};

function findFirstFile(nodes: ArtifactTreeNode[]): ArtifactTreeNode | null {
  for (const node of nodes) {
    if (node.type === 'file') {
      return node;
    }
    if (node.children) {
      const child = findFirstFile(node.children);
      if (child) {
        return child;
      }
    }
  }
  return null;
}

function flattenNodes(nodes: ArtifactTreeNode[]): ArtifactTreeNode[] {
  const flat: ArtifactTreeNode[] = [];
  for (const node of nodes) {
    flat.push(node);
    if (node.children) {
      flat.push(...flattenNodes(node.children));
    }
  }
  return flat;
}

function TreeNode({
  node,
  depth,
  navigation,
  selectedPath,
}: {
  node: ArtifactTreeNode;
  depth: number;
  navigation: ReturnType<typeof useArtifactTreeNavigation>;
  selectedPath: string | null;
}) {
  const isDir = node.type === 'directory';
  const isOpen = isDir && navigation.expandedPaths.has(node.path);
  const isSelected = !isDir && selectedPath === node.path;

  const Icon = isDir
    ? isOpen
      ? FolderOpen
      : Folder
    : node.path.match(/\.(md|markdown|mdown|mkd)$/i)
      ? FileText
      : node.path.match(/\.(html?|xhtml)$/i)
        ? FileCode
        : node.path.match(/\.(png|jpe?g|gif|webp|svg|bmp|ico)$/i)
          ? FileImage
          : File;

  return (
    <div {...navigation.getItemProps(node)}>
      <div
        className={cn(
          'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors',
          isSelected
            ? 'bg-primary/10 text-primary'
            : 'text-foreground hover:bg-muted'
        )}
        style={{ paddingLeft: `${depth * 14 + 8}px` }}
      >
        <Icon className="h-4 w-4 shrink-0" />
        <span className="min-w-0 flex-1 truncate">{node.name}</span>
        {!isDir && node.size != null && (
          <span className="shrink-0 text-[11px] text-muted-foreground">
            {Intl.NumberFormat().format(node.size)}
          </span>
        )}
      </div>
      {isDir && isOpen && node.children && node.children.length > 0 && (
        <div role="group" className="space-y-0.5">
          {node.children.map((child) => (
            <TreeNode
              key={child.path}
              node={child}
              depth={depth + 1}
              navigation={navigation}
              selectedPath={selectedPath}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export default function ArtifactsTab({
  dagRun,
  artifactEnabled = false,
  className,
  fillHeight = false,
}: Props) {
  const { ts } = useI18n();
  const client = useClient();
  const remoteNode = useRemoteNode();
  const isSubDAGRun =
    !!dagRun.rootDAGRunId &&
    dagRun.rootDAGRunId !== dagRun.dagRunId &&
    !!dagRun.rootDAGRunName;

  const [tree, setTree] = useState<ArtifactTreeNode[]>([]);
  const [treeLoading, setTreeLoading] = useState(false);
  const [treeError, setTreeError] = useState<string | null>(null);
  const [storedSelectedPath, setSelectedPath] = useState<string | null>(null);
  const [previewVersion, setPreviewVersion] = useState(0);
  const treeRequestRef = useRef<{
    id: number;
    controller: AbortController | null;
  }>({ id: 0, controller: null });

  const allNodes = useMemo(() => flattenNodes(tree), [tree]);
  const scope = JSON.stringify([
    remoteNode,
    dagRun.name,
    dagRun.dagRunId,
    dagRun.rootDAGRunName,
    dagRun.rootDAGRunId,
  ]);
  const loadedScopeRef = useRef<string | null>(null);
  const selectedPath =
    loadedScopeRef.current === scope ? storedSelectedPath : null;
  const navigation = useArtifactTreeNavigation({
    nodes: tree,
    selectedPath,
    onSelect: setSelectedPath,
    scope,
    ready: !treeLoading,
  });

  const requestArtifactTree = async (signal?: AbortSignal) => {
    if (isSubDAGRun) {
      return client.GET(
        '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/artifacts',
        {
          params: {
            path: {
              name: dagRun.rootDAGRunName!,
              dagRunId: dagRun.rootDAGRunId!,
              subDAGRunId: dagRun.dagRunId,
            },
            query: { remoteNode, recursive: true },
          },
          signal,
        }
      );
    }

    return client.GET('/dag-runs/{name}/{dagRunId}/artifacts', {
      params: {
        path: {
          name: dagRun.name,
          dagRunId: dagRun.dagRunId,
        },
        query: { remoteNode, recursive: true },
      },
      signal,
    });
  };

  const fetchTree = async () => {
    const requestId = treeRequestRef.current.id + 1;
    treeRequestRef.current.controller?.abort();

    if (!dagRun.artifactsAvailable) {
      treeRequestRef.current = { id: requestId, controller: null };
      setTree([]);
      setSelectedPath(null);
      setTreeError(null);
      setTreeLoading(false);
      return;
    }

    const controller = new AbortController();
    treeRequestRef.current = { id: requestId, controller };

    const isCurrentRequest = () =>
      treeRequestRef.current.id === requestId &&
      treeRequestRef.current.controller === controller &&
      !controller.signal.aborted;

    setTreeLoading(true);
    setTreeError(null);
    if (loadedScopeRef.current !== scope) {
      setSelectedPath(null);
    }
    try {
      const request = await requestArtifactTree(controller.signal);

      if (!isCurrentRequest()) {
        return;
      }

      if (request.error) {
        setTree([]);
        setSelectedPath(null);
        setTreeError(request.error.message || 'Failed to load artifacts');
        return;
      }

      const items = request.data?.items ?? [];
      const nextNodes = flattenNodes(items);
      setTree(items);

      const sameScope = loadedScopeRef.current === scope;
      loadedScopeRef.current = scope;
      const firstFile = findFirstFile(items);
      if (!firstFile) {
        setSelectedPath(null);
        return;
      }

      if (
        sameScope &&
        selectedPath &&
        nextNodes.some((node) => node.path === selectedPath)
      ) {
        setPreviewVersion((current) => current + 1);
        return;
      }

      setSelectedPath(firstFile.path);
    } catch (error: unknown) {
      if (controller.signal.aborted || !isCurrentRequest()) {
        return;
      }

      setTree([]);
      setSelectedPath(null);
      setTreeError(
        error instanceof Error ? error.message : 'Failed to load artifacts'
      );
    } finally {
      if (
        treeRequestRef.current.id === requestId &&
        treeRequestRef.current.controller === controller
      ) {
        setTreeLoading(false);
        treeRequestRef.current = { id: requestId, controller: null };
      }
    }
  };

  useEffect(() => {
    void fetchTree();

    return () => {
      treeRequestRef.current.controller?.abort();
    };
  }, [
    client,
    dagRun.artifactsAvailable,
    dagRun.dagRunId,
    dagRun.name,
    dagRun.rootDAGRunId,
    dagRun.rootDAGRunName,
    isSubDAGRun,
    remoteNode,
  ]);

  if (!artifactEnabled && !dagRun.artifactsAvailable) {
    return (
      <div className="rounded-lg border border-dashed border-border bg-muted/20 p-6 text-sm text-muted-foreground">
        <I18nText text={'Artifact storage is not enabled for this DAG run.'} />
      </div>
    );
  }

  if (!dagRun.artifactsAvailable) {
    return (
      <div className="rounded-lg border border-dashed border-border bg-muted/20 p-6 text-sm text-muted-foreground">
        <I18nTemplate
          text="Artifacts will appear here after a run writes files into {directory}."
          values={{
            directory: (
              <code className="mx-1 rounded bg-muted px-1.5 py-0.5 text-xs">
                DAG_RUN_ARTIFACTS_DIR
              </code>
            ),
          }}
        />
      </div>
    );
  }

  return (
    <div
      className={cn(
        'grid grid-cols-1 gap-4 xl:grid-cols-[320px_minmax(0,1fr)]',
        fillHeight && 'h-full min-h-0',
        className
      )}
    >
      <div
        className={cn(
          'rounded-lg border border-border bg-surface',
          fillHeight && 'flex min-h-0 flex-col overflow-hidden'
        )}
      >
        <div className="flex items-center justify-between border-b border-border px-3 py-2">
          <div>
            <p className="text-sm font-medium">
              <I18nText text={'Artifacts'} />
            </p>
            <p className="text-xs text-muted-foreground">
              {tree.length === 0 ? (
                <I18nText text={'No files yet'} />
              ) : (
                ts('{count} files', {
                  count: allNodes.filter((node) => node.type === 'file').length,
                })
              )}
            </p>
          </div>
          <I18nProps>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => {
                void fetchTree();
              }}
              title="Reload artifacts"
            >
              <RefreshCw
                className={cn('h-4 w-4', treeLoading && 'animate-spin')}
              />
            </Button>
          </I18nProps>
        </div>

        <div
          className={cn(
            'overflow-auto p-2',
            fillHeight ? 'min-h-0 flex-1' : 'max-h-[34rem]'
          )}
        >
          {treeLoading ? (
            <div className="px-2 py-6 text-sm text-muted-foreground">
              <I18nText text={'Loading artifacts...'} />
            </div>
          ) : treeError ? (
            <div className="flex items-start gap-2 rounded-md bg-destructive/5 px-3 py-3 text-sm text-destructive">
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{treeError}</span>
            </div>
          ) : tree.length === 0 ? (
            <div className="px-2 py-6 text-sm text-muted-foreground">
              <I18nText
                text={'No artifacts have been written for this run yet.'}
              />
            </div>
          ) : (
            <div
              {...navigation.treeProps}
              aria-label={ts('Artifacts')}
              className="space-y-0.5"
            >
              {tree.map((node) => (
                <TreeNode
                  key={node.path}
                  node={node}
                  depth={0}
                  navigation={navigation}
                  selectedPath={selectedPath}
                />
              ))}
            </div>
          )}
        </div>
        {tree.length > 0 && (
          <p className="px-3 pb-2 text-xs text-muted-foreground">
            <I18nText text="↑↓ / j k navigate · ←→ folders · Enter preview" />
          </p>
        )}
      </div>

      <ArtifactFilePreview
        contentRef={navigation.previewRef}
        onReturnToFiles={navigation.returnToTree}
        dagRunName={isSubDAGRun ? dagRun.rootDAGRunName! : dagRun.name}
        dagRunId={isSubDAGRun ? dagRun.rootDAGRunId! : dagRun.dagRunId}
        subDAGRunId={isSubDAGRun ? dagRun.dagRunId : null}
        path={selectedPath}
        remoteNode={remoteNode}
        version={previewVersion}
        fillHeight={fillHeight}
      />
    </div>
  );
}
