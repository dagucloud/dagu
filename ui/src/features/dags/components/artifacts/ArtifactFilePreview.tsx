// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { WikiPageMarkdownPreview } from '@/components/ui/wiki-page-markdown-preview';
import { useClient } from '@/hooks/api';
import { downloadBlob } from '@/lib/download';
import { cn } from '@/lib/utils';
import {
  AlertCircle,
  Check,
  ClipboardCopy,
  Download,
} from 'lucide-react';
import React, { useEffect, useState } from 'react';
import { components } from '../../../../api/v1/schema';
import { fetchArtifactDownload as fetchArtifact } from './artifactDownload';
import { HtmlArtifactPreview } from './HtmlArtifactPreview';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

type ArtifactPreviewResponse = components['schemas']['ArtifactPreviewResponse'];

type Props = {
  dagRunName: string;
  dagRunId: string;
  subDAGRunId?: string | null;
  path: string | null;
  remoteNode: string;
  version?: number;
  className?: string;
  fillHeight?: boolean;
  contentRef?: React.Ref<HTMLDivElement>;
  onReturnToFiles?: () => void;
};

export function ArtifactFilePreview({
  dagRunName,
  dagRunId,
  subDAGRunId = null,
  path,
  remoteNode,
  version = 0,
  className,
  fillHeight = false,
  contentRef,
  onReturnToFiles,
}: Props) {
  const { ts } = useI18n();
  const client = useClient();
  const isSubDAGRun = !!subDAGRunId;

  const [preview, setPreview] = useState<ArtifactPreviewResponse | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [imageUrl, setImageUrl] = useState<string | null>(null);
  const [markdownViewMode, setMarkdownViewMode] = useState<'preview' | 'raw'>(
    'preview'
  );
  const [htmlViewMode, setHTMLViewMode] = useState<'preview' | 'raw'>(
    'preview'
  );
  const [copiedContent, setCopiedContent] = useState(false);

  const selectedName = path?.split('/').pop() || '';
  const isMarkdownPreview = preview?.kind === 'markdown';
  const isHTMLPreview = preview?.kind === 'html';
  const isMarkupPreview = isMarkdownPreview || isHTMLPreview;
  const markupViewMode = isHTMLPreview ? htmlViewMode : markdownViewMode;
  const isCopyablePreview =
    preview?.kind === 'markdown' ||
    preview?.kind === 'html' ||
    preview?.kind === 'text';
  const previewTruncatedNotice =
    preview?.truncated &&
    (preview.kind === 'markdown' ||
      preview.kind === 'html' ||
      preview.kind === 'text');

  const requestArtifactPreview = async (selectedPath: string) => {
    if (isSubDAGRun) {
      return client.GET(
        '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/artifacts/preview',
        {
          params: {
            path: {
              name: dagRunName,
              dagRunId,
              subDAGRunId: subDAGRunId!,
            },
            query: { remoteNode, path: selectedPath },
          },
        }
      );
    }

    return client.GET('/dag-runs/{name}/{dagRunId}/artifacts/preview', {
      params: {
        path: {
          name: dagRunName,
          dagRunId,
        },
        query: { remoteNode, path: selectedPath },
      },
    });
  };

  const fetchArtifactDownload = (selectedPath: string, signal?: AbortSignal) =>
    fetchArtifact(
      client,
      { dagRunName, dagRunId, subDAGRunId, remoteNode },
      selectedPath,
      signal
    );

  useEffect(() => {
    if (!path) {
      setPreview(null);
      setPreviewError(null);
      return;
    }

    let cancelled = false;
    setPreviewLoading(true);
    setPreviewError(null);

    const loadPreview = async () => {
      try {
        const request = await requestArtifactPreview(path);

        if (cancelled) {
          return;
        }

        if (request.error) {
          setPreview(null);
          setPreviewError(
            request.error.message || 'Failed to load artifact preview'
          );
          return;
        }

        setPreview(request.data ?? null);
      } catch (error: unknown) {
        if (cancelled) {
          return;
        }
        setPreview(null);
        setPreviewError(
          error instanceof Error
            ? error.message
            : 'Failed to load artifact preview'
        );
      } finally {
        if (!cancelled) {
          setPreviewLoading(false);
        }
      }
    };

    void loadPreview();

    return () => {
      cancelled = true;
    };
  }, [client, dagRunId, dagRunName, path, remoteNode, subDAGRunId, version]);

  useEffect(() => {
    if (
      !preview ||
      preview.kind !== 'image' ||
      preview.tooLarge ||
      !path
    ) {
      setImageUrl(null);
      return;
    }

    let cancelled = false;
    let objectUrl = '';
    const controller = new AbortController();

    const loadImage = async () => {
      const request = await fetchArtifactDownload(path, controller.signal);
      if (cancelled) {
        return;
      }

      objectUrl = URL.createObjectURL(request.data);
      setImageUrl(objectUrl);
    };

    void loadImage().catch((error: unknown) => {
      if (cancelled) {
        return;
      }
      setPreviewError(
        error instanceof Error ? error.message : 'Failed to load image preview'
      );
    });

    return () => {
      cancelled = true;
      controller.abort();
      if (objectUrl) {
        URL.revokeObjectURL(objectUrl);
      }
    };
  }, [client, dagRunId, dagRunName, path, preview, remoteNode, subDAGRunId]);

  const handleDownload = async () => {
    if (!path) {
      return;
    }

    const request = await fetchArtifactDownload(path);
    const fileName =
      request.response.headers
        .get('Content-Disposition')
        ?.match(/filename="(.+)"/)?.[1] ||
      selectedName ||
      'artifact';
    downloadBlob(request.data, fileName);
  };

  const handleCopyContent = async () => {
    if (!preview || !path || !isCopyablePreview) {
      return;
    }

    let text = preview.content ?? '';
    if (preview.truncated || preview.tooLarge || preview.content == null) {
      const request = await fetchArtifactDownload(path);
      text = await request.data.text();
    }

    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const textArea = document.createElement('textarea');
      textArea.value = text;
      document.body.appendChild(textArea);
      textArea.select();
      document.execCommand('copy');
      document.body.removeChild(textArea);
    }

    setCopiedContent(true);
    window.setTimeout(() => setCopiedContent(false), 2000);
  };

  return (
    <div
      onKeyDown={(event) => {
        if (
          event.key === 'Escape' &&
          !event.defaultPrevented &&
          !event.nativeEvent.isComposing &&
          onReturnToFiles
        ) {
          event.preventDefault();
          event.stopPropagation();
          onReturnToFiles();
        }
      }}
      className={cn(
        'rounded-lg border border-border bg-background',
        fillHeight && 'flex min-h-0 flex-col overflow-hidden',
        className
      )}
    >
      <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">
            {selectedName || <I18nText text={'Select an artifact'} />}
          </p>
          <p className="truncate text-xs text-muted-foreground">
            {path || <I18nText text={'Choose a file from the left panel'} />}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {onReturnToFiles && (
            <Button variant="ghost" size="sm" onClick={onReturnToFiles}>
              <I18nText text="Back to files" />
            </Button>
          )}
          {isCopyablePreview ? (
            <I18nProps>
              <button
                type="button"
                onClick={() => {
                  void handleCopyContent().catch((error: unknown) => {
                    setPreviewError(
                      error instanceof Error
                        ? error.message
                        : 'Failed to copy artifact contents'
                    );
                  });
                }}
                className="flex items-center gap-1 rounded-md px-2 py-0.5 text-xs text-muted-foreground transition-all hover:bg-muted hover:text-foreground"
                title="Copy content"
              >
                {copiedContent ? (
                  <Check className="h-3 w-3 text-green-500" />
                ) : (
                  <ClipboardCopy className="h-3 w-3" />
                )}
                <span>
                  <I18nText text={'Copy'} />
                </span>
              </button>
            </I18nProps>
          ) : null}
          {isMarkupPreview ? (
            <div className="flex overflow-hidden rounded-md border border-border">
              <button
                type="button"
                className={cn(
                  'px-2 py-0.5 text-xs transition-colors',
                  markupViewMode === 'preview'
                    ? 'bg-accent text-accent-foreground'
                    : 'text-muted-foreground hover:text-foreground'
                )}
                onClick={() => {
                  if (isHTMLPreview) {
                    setHTMLViewMode('preview');
                    return;
                  }
                  setMarkdownViewMode('preview');
                }}
              >
                <I18nText text={'Preview'} />
              </button>
              <button
                type="button"
                className={cn(
                  'px-2 py-0.5 text-xs transition-colors',
                  markupViewMode === 'raw'
                    ? 'bg-accent text-accent-foreground'
                    : 'text-muted-foreground hover:text-foreground'
                )}
                onClick={() => {
                  if (isHTMLPreview) {
                    setHTMLViewMode('raw');
                    return;
                  }
                  setMarkdownViewMode('raw');
                }}
              >
                <I18nText text={'Raw'} />
              </button>
            </div>
          ) : null}
          <Button
            variant="outline"
            size="sm"
            disabled={!path}
            onClick={() => {
              void handleDownload().catch((error: unknown) => {
                setPreviewError(
                  error instanceof Error ? error.message : 'Download failed'
                );
              });
            }}
          >
            <Download className="h-4 w-4" />
            <I18nText text={'Download'} />
          </Button>
        </div>
      </div>

      <div
        ref={contentRef}
        role="region"
        aria-label={selectedName || ts('Artifact preview')}
        tabIndex={-1}
        className={cn(
          'overflow-auto p-4 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring',
          fillHeight ? 'min-h-0 flex-1' : 'max-h-[34rem]'
        )}
      >
        {!path ? (
          <div className="text-sm text-muted-foreground">
            <I18nText text={'Select a file to preview it.'} />
          </div>
        ) : previewLoading ? (
          <div className="text-sm text-muted-foreground">
            <I18nText text={'Loading preview...'} />
          </div>
        ) : previewError ? (
          <div className="flex items-start gap-2 rounded-md bg-destructive/5 px-3 py-3 text-sm text-destructive">
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
            <span>{previewError}</span>
          </div>
        ) : !preview ? (
          <div className="text-sm text-muted-foreground">
            <I18nText text={'Preview unavailable.'} />
          </div>
        ) : preview.tooLarge ? (
          <div className="rounded-md border border-dashed border-border bg-muted/20 p-6">
            <p className="text-sm font-medium">
              <I18nText text={'Preview unavailable'} />
            </p>
            <p className="mt-1 text-sm text-muted-foreground">
              <I18nText
                text={
                  'This artifact is too large to render inline. Download it to inspect the contents.'
                }
              />
            </p>
            <dl className="mt-4 space-y-1 text-xs text-muted-foreground">
              <div>
                <dt className="inline font-medium text-foreground">
                  <I18nText text={'MIME:'} />
                </dt>{' '}
                <dd className="inline">{preview.mimeType}</dd>
              </div>
              <div>
                <dt className="inline font-medium text-foreground">
                  <I18nText text={'Size:'} />
                </dt>{' '}
                <dd className="inline">
                  {Intl.NumberFormat().format(preview.size)}{' '}
                  <I18nText text={'bytes'} />
                </dd>
              </div>
            </dl>
          </div>
        ) : preview.kind === 'markdown' ? (
          <div className="space-y-3">
            {previewTruncatedNotice ? (
              <div className="rounded-md border border-dashed border-border bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
                <I18nText
                  text={
                    'Inline preview is truncated. Use Copy or Download for the full file.'
                  }
                />
              </div>
            ) : null}
            {markdownViewMode === 'raw' ? (
              <pre className="overflow-auto rounded-md border border-border bg-muted/20 p-4 text-sm leading-6 whitespace-pre-wrap">
                {preview.content || ''}
              </pre>
            ) : (
              <WikiPageMarkdownPreview content={preview.content} />
            )}
          </div>
        ) : preview.kind === 'html' ? (
          <div
            className={cn('space-y-3', fillHeight && 'flex min-h-0 flex-col')}
          >
            {previewTruncatedNotice ? (
              <div className="rounded-md border border-dashed border-border bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
                <I18nText
                  text={
                    'Inline preview is truncated. Use Copy or Download for the full file.'
                  }
                />
              </div>
            ) : null}
            {htmlViewMode === 'raw' ? (
              <pre className="overflow-auto rounded-md border border-border bg-muted/20 p-4 text-sm leading-6 whitespace-pre-wrap">
                {preview.content || ''}
              </pre>
            ) : (
              <HtmlArtifactPreview
                content={preview.content}
                fillHeight={fillHeight}
                className={fillHeight ? 'min-h-0 flex-1' : undefined}
              />
            )}
          </div>
        ) : preview.kind === 'text' ? (
          <div className="space-y-3">
            {previewTruncatedNotice ? (
              <div className="rounded-md border border-dashed border-border bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
                <I18nText
                  text={
                    'Inline preview is truncated. Use Copy or Download for the full file.'
                  }
                />
              </div>
            ) : null}
            <pre className="overflow-auto rounded-md border border-border bg-muted/20 p-4 text-sm leading-6 whitespace-pre-wrap">
              {preview.content || ''}
            </pre>
          </div>
        ) : preview.kind === 'image' ? (
          imageUrl ? (
            <img
              src={imageUrl}
              alt={preview.name}
              className={cn(
                'max-w-full rounded-md border border-border object-contain',
                fillHeight ? 'max-h-full' : 'max-h-[40rem]'
              )}
            />
          ) : (
            <div className="text-sm text-muted-foreground">
              <I18nText text={'Loading image preview...'} />
            </div>
          )
        ) : (
          <div className="rounded-md border border-dashed border-border bg-muted/20 p-6">
            <p className="text-sm font-medium">
              <I18nText text={'Binary artifact'} />
            </p>
            <p className="mt-1 text-sm text-muted-foreground">
              <I18nText
                text={
                  'This file can’t be rendered inline. Download it to inspect the contents.'
                }
              />
            </p>
            <dl className="mt-4 space-y-1 text-xs text-muted-foreground">
              <div>
                <dt className="inline font-medium text-foreground">
                  <I18nText text={'MIME:'} />
                </dt>{' '}
                <dd className="inline">{preview.mimeType}</dd>
              </div>
              <div>
                <dt className="inline font-medium text-foreground">
                  <I18nText text={'Size:'} />
                </dt>{' '}
                <dd className="inline">
                  {Intl.NumberFormat().format(preview.size)}{' '}
                  <I18nText text={'bytes'} />
                </dd>
              </div>
            </dl>
          </div>
        )}
      </div>
    </div>
  );
}
