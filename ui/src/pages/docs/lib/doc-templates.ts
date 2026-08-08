// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Prefix under which user-defined document templates live. Any document
 * under this folder (per workspace or at the root) appears in the create
 * dialog's template picker.
 */
export const DOC_TEMPLATES_PREFIX = '_templates';

/** Placeholder replaced with a DAG name when a template is created for a DAG. */
export const DOC_TEMPLATE_DAG_NAME = 'DAG_NAME';

export type DocTemplate = {
  id: string;
  name: string;
  description: string;
  content: string;
  /** Document path backing a user template; undefined for built-ins. */
  path?: string;
  /** Workspace holding a user template; undefined for built-ins. */
  workspace?: string | null;
  builtIn: boolean;
};

export const BUILT_IN_DOC_TEMPLATES: DocTemplate[] = [
  {
    id: 'builtin:runbook',
    name: 'Runbook',
    description: 'Operational runbook with live DAG status and run actions',
    builtIn: true,
    content: `---
title: Runbook
description: What this runbook recovers and when to use it.
tags: [runbook]
---

# Runbook

Status: [[dag:${DOC_TEMPLATE_DAG_NAME}]]

## When to use

Describe the symptom that brings an operator here.

## Steps

1. Check the latest run and its logs.
2. Fix the underlying issue.
3. Re-run the workflow:

\`\`\`dagu-run
dag: ${DOC_TEMPLATE_DAG_NAME}
label: Re-run after fixing the issue
confirm: Starts a new run. Make sure the underlying issue is fixed first.
\`\`\`

> For actions needing sign-off, add an \`action: human.task\` step to the
> target DAG; the run then waits for approval before continuing.

## Escalation

Who to contact when the steps above do not resolve the issue.
`,
  },
  {
    id: 'builtin:postmortem',
    name: 'Postmortem',
    description: 'Incident postmortem with timeline and follow-ups',
    builtIn: true,
    content: `---
title: Postmortem
description: What happened, impact, and how recurrence is prevented.
tags: [postmortem]
---

# Postmortem

## Summary

One paragraph: what broke, for how long, and the user impact.

## Timeline

| Time (UTC) | Event |
| --- | --- |
|  |  |

## Root cause

## Resolution

## Follow-ups

- [ ]
`,
  },
  {
    id: 'builtin:adr',
    name: 'Architecture Decision',
    description: 'Architecture decision record',
    builtIn: true,
    content: `---
title: ADR
description: Decision, context, and consequences.
tags: [adr]
---

# ADR: Title

## Status

Proposed.

## Context

## Decision

## Consequences
`,
  },
  {
    id: 'builtin:onboarding',
    name: 'Onboarding',
    description: 'Team onboarding guide',
    builtIn: true,
    content: `---
title: Onboarding
description: Start here.
tags: [onboarding]
---

# Onboarding

## Key workflows

Link the workflows this team operates, for example [[dag:example]].

## Key documents

## First tasks
`,
  },
];
