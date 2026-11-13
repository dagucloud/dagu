// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { backfillPath, backfillRunsPath, workflowPath } from '../backfillLinks';

describe('backfill links', () => {
  // A page without the node takes the one the app bar selects, which may be
  // another than the node of the link, so the local node is named too.
  it('name the node of the link, the local one included', () => {
    expect(backfillPath('bf123456', 'local')).toBe(
      '/backfills/bf123456?remoteNode=local'
    );
    expect(backfillRunsPath('bf123456', 'edge')).toBe(
      '/dag-runs?backfillId=bf123456&remoteNode=edge'
    );
    expect(workflowPath('my etl', 'edge')).toBe(
      '/dags/my%20etl?remoteNode=edge'
    );
  });
});
