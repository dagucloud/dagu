// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Download, File as FileIcon } from 'lucide-react';
import { useEffect, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { baseName, formatBytes, imageType } from '@/features/skills/skillFiles';
import type { SkillFile } from '@/features/skills/skillIssues';
import { readSkillFileBlob } from '@/features/skills/skillSync';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { downloadBlob } from '@/lib/download';

/**
 * A file that the editor does not open as text: its name, its size and a
 * download button. A PNG, JPEG, GIF or WebP image shows a preview.
 */
export function SkillBinaryView({
  name,
  remoteNode,
  file,
}: {
  name: string;
  remoteNode: string;
  file: SkillFile;
}): ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const [preview, setPreview] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const target = { name, remoteNode };
  const type = imageType(file.path);

  useEffect(() => {
    if (!type) return;
    let cancelled = false;
    let url = '';
    const controller = new AbortController();
    readSkillFileBlob(
      client,
      { name, remoteNode },
      file.path,
      controller.signal
    )
      .then((blob) => {
        if (cancelled) return;
        url = URL.createObjectURL(new Blob([blob], { type }));
        setPreview(url);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
      controller.abort();
      if (url) URL.revokeObjectURL(url);
      setPreview(null);
    };
  }, [client, name, remoteNode, file.path, file.sha256, type]);

  const download = async () => {
    setBusy(true);
    setProblem(null);
    try {
      const blob = await readSkillFileBlob(client, target, file.path);
      downloadBlob(blob, baseName(file.path));
    } catch (failure) {
      setProblem(
        failure instanceof Error
          ? failure.message
          : ts('The file could not be downloaded.')
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      data-testid="skill-binary"
      className="flex h-full flex-col items-start gap-3 overflow-y-auto p-4"
    >
      <div className="flex min-w-0 items-center gap-2">
        <FileIcon
          className="size-5 shrink-0 text-muted-foreground"
          aria-hidden
        />
        <div className="min-w-0">
          <p className="truncate font-mono text-sm font-medium">{file.path}</p>
          <p className="text-xs text-muted-foreground">
            {formatBytes(file.size)}
          </p>
        </div>
      </div>
      {preview && (
        <img
          src={preview}
          alt={baseName(file.path)}
          className="max-h-64 max-w-full rounded border border-border bg-muted object-contain"
        />
      )}
      <p className="text-xs text-muted-foreground">
        {ts('The editor does not open this file. Download it to read it.')}
      </p>
      <Button
        variant="outline"
        size="sm"
        disabled={busy}
        onClick={() => void download()}
      >
        <Download aria-hidden="true" />
        {ts('Download')}
      </Button>
      {problem && (
        <p role="alert" className="text-xs text-destructive">
          {problem}
        </p>
      )}
    </div>
  );
}
