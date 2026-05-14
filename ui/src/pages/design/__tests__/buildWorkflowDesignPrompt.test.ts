// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { buildWorkflowDesignPrompt } from '../buildWorkflowDesignPrompt';

describe('buildWorkflowDesignPrompt', () => {
  it('builds an update prompt with DAG and step context', () => {
    const prompt = buildWorkflowDesignPrompt({
      mode: 'update',
      dagFile: 'daily-report',
      stepName: 'fetch-data',
      remoteNode: 'local',
      userPrompt: 'Retry this step twice and make the timeout explicit.',
      validationErrors: ['timeout must be a duration'],
    });

    expect(prompt).toContain('Mode: Update existing DAG');
    expect(prompt).toContain('Target DAG: daily-report');
    expect(prompt).toContain('Target step: fetch-data');
    expect(prompt).toContain('Retry this step twice');
    expect(prompt).toContain('timeout must be a duration');
    expect(prompt).toContain('Make the file changes directly');
    expect(prompt).toContain('/design?dag=daily-report');
  });

  it('builds a create prompt with the current draft YAML', () => {
    const prompt = buildWorkflowDesignPrompt({
      mode: 'create',
      newDagName: 'daily-report',
      remoteNode: 'worker-a',
      userPrompt: 'Create a DAG that writes a daily summary.',
      draftSpec: 'steps:\n  - name: summarize\n    run: echo ok\n',
    });

    expect(prompt).toContain('Mode: Create new DAG');
    expect(prompt).toContain('Remote node: worker-a');
    expect(prompt).toContain('Target DAG: daily-report');
    expect(prompt).toContain('Current draft YAML');
    expect(prompt).toContain('```yaml');
    expect(prompt).toContain('name: summarize');
  });
});
