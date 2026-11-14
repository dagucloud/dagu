// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { useClient } from '../../../hooks/api';

type APIClient = ReturnType<typeof useClient>;

/** Pins or unpins a workflow for all users. Throws when the request fails. */
export async function setDAGPinned(
  client: APIClient,
  {
    fileName,
    pinned,
    remoteNode,
  }: { fileName: string; pinned: boolean; remoteNode: string }
): Promise<void> {
  const init = { params: { path: { fileName }, query: { remoteNode } } };
  const { error } = pinned
    ? await client.PUT('/dags/{fileName}/pin', init)
    : await client.DELETE('/dags/{fileName}/pin', init);
  if (error) {
    throw new Error(
      error.message ||
        (pinned ? 'Failed to pin workflow' : 'Failed to unpin workflow')
    );
  }
}
