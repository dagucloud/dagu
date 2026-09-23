// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useClient } from '@/hooks/api';
import React from 'react';
import {
  type ArtifactRunRef,
  fetchArtifactDownload,
} from '../artifacts/artifactDownload';

const imagePattern = /\.(png|jpe?g|gif|webp)$/i;

/** Reports whether a run artifact path names an image. */
export function isImageArtifact(path: string) {
  return imagePattern.test(path);
}

/**
 * Shows a run artifact image as a thumbnail that opens full size in a new
 * tab. Falls back to the path when the image cannot be loaded.
 */
export function ArtifactThumbnail({
  run,
  path,
}: {
  run: ArtifactRunRef;
  path: string;
}) {
  const client = useClient();
  const [url, setUrl] = React.useState<string>();
  const [failed, setFailed] = React.useState(false);

  const { dagRunName, dagRunId, subDAGRunId, remoteNode } = run;

  React.useEffect(() => {
    let objectUrl = '';
    const controller = new AbortController();
    setUrl(undefined);
    setFailed(false);
    fetchArtifactDownload(
      client,
      { dagRunName, dagRunId, subDAGRunId, remoteNode },
      path,
      controller.signal
    )
      .then((request) => {
        // A fetch that settles after cleanup must not create a URL that
        // nothing revokes.
        if (controller.signal.aborted) return;
        objectUrl = URL.createObjectURL(request.data);
        setUrl(objectUrl);
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(true);
      });
    return () => {
      controller.abort();
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [client, dagRunId, dagRunName, path, remoteNode, subDAGRunId]);

  const name = path.split('/').pop() || path;
  if (failed || !url) {
    return (
      <div
        className="truncate font-mono text-xs text-muted-foreground"
        title={path}
      >
        {name}
      </div>
    );
  }
  return (
    <a
      href={url}
      target="_blank"
      rel="noreferrer"
      title={path}
      className="inline-block"
    >
      <img
        src={url}
        alt={name}
        className="h-24 max-w-[12rem] rounded border border-border object-cover object-top"
      />
    </a>
  );
}
