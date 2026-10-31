// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { actionDescription, actionNamespace, actionTitle } from '../actionText';

describe('actionNamespace', () => {
  it.each([
    ['git.worktree.add', 'git'],
    ['http.request', 'http'],
    ['exec', 'exec'],
    ['redis.get', 'redis'],
  ])('%s is in %s', (name, namespace) => {
    expect(actionNamespace(name)).toBe(namespace);
  });
});

describe('actionTitle', () => {
  it.each([
    ['http.request', 'HTTP request'],
    ['git.worktree.add', 'Git worktree add'],
    ['dag.run', 'DAG run'],
    ['xlsx.list_sheets', 'XLSX list sheets'],
    ['k8s.run', 'K8s run'],
    ['ssh.run', 'SSH run'],
    ['s3.upload', 'S3 upload'],
    ['jq.filter', 'Jq filter'],
    ['send-slack', 'Send slack'],
    ['noop', 'Noop'],
    // A key that every object has is a word, not an acronym.
    ['constructor.run', 'Constructor run'],
  ])('%s gives %s', (name, title) => {
    expect(actionTitle(name)).toBe(title);
  });
});

describe('actionDescription', () => {
  it.each([
    ['Configuration for action: dag.run.', undefined],
    [
      'Configuration for action: clickhouse.query. Statements run over HTTP.',
      'Statements run over HTTP.',
    ],
    ['Configuration for action: xlsx.read, xlsx.info, xlsx.write.', undefined],
    [
      'Configuration for local file actions.',
      'Configuration for local file actions.',
    ],
    ['', undefined],
    [undefined, undefined],
  ])('%j gives %j', (text, expected) => {
    expect(actionDescription(text)).toBe(expected);
  });
});
