// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Box,
  Boxes,
  Braces,
  Cloud,
  Container,
  Database,
  File,
  GitBranch,
  Globe,
  Hourglass,
  Mail,
  MessageSquare,
  Server,
  Split,
  Terminal,
  Workflow,
} from 'lucide-react';
import { describe, expect, it } from 'vitest';
import { actionIcon, actionNamespace } from '../icons';

describe('actionNamespace', () => {
  it('returns the text before the first dot', () => {
    expect(actionNamespace('http.request')).toBe('http');
    expect(actionNamespace('a.b.c')).toBe('a');
    expect(actionNamespace('run')).toBe('run');
  });

  it('returns null without an action', () => {
    expect(actionNamespace(undefined)).toBeNull();
    expect(actionNamespace('')).toBeNull();
    expect(actionNamespace('.x')).toBeNull();
  });
});

describe('actionIcon', () => {
  it.each([
    ['http.request', Globe],
    ['s3.upload', Cloud],
    ['file.read', File],
    ['git.checkout', GitBranch],
    ['docker.run', Container],
    ['container.run', Container],
    ['k8s.apply', Boxes],
    ['kubernetes.job', Boxes],
    ['mail.send', Mail],
    ['ssh.exec', Server],
    ['sql.query', Database],
    ['postgres.query', Database],
    ['mysql.query', Database],
    ['sqlite.query', Database],
    ['duckdb.query', Database],
    ['wait.duration', Hourglass],
    ['router.route', Split],
    ['dag.run', Workflow],
    ['chat.completion', MessageSquare],
    ['jq.filter', Braces],
    ['my-action', Box],
  ])('maps %s', (action, icon) => {
    expect(actionIcon({ kind: 'action', action })).toBe(icon);
  });

  it('lets the kind decide first', () => {
    expect(actionIcon({ kind: 'router' })).toBe(Split);
    expect(actionIcon({ kind: 'call' })).toBe(Workflow);
    expect(actionIcon({ kind: 'run' })).toBe(Terminal);
  });

  it('falls back to the default icon', () => {
    expect(actionIcon({ kind: 'action' })).toBe(Box);
    expect(actionIcon({ kind: 'other' })).toBe(Box);
  });

  // The namespace is user text: a key that every object has is no icon.
  it('gives the default icon for an inherited key', () => {
    expect(actionIcon({ kind: 'action', action: 'constructor.run' })).toBe(Box);
    expect(actionIcon({ kind: 'action', action: '__proto__.run' })).toBe(Box);
  });
});
