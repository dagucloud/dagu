// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it } from 'vitest';
import { DocMarkdownPreview } from '../doc-markdown-preview';

describe('DocMarkdownPreview', () => {
  it('hides YAML frontmatter from the rendered preview', () => {
    const { container } = render(
      <DocMarkdownPreview
        content={`---
title: Restart API
description: Restart the API service and verify health.
---

# Restart API

Follow the restart procedure.`}
      />
    );

    expect(container.textContent).not.toContain('title: Restart API');
    expect(container.textContent).not.toContain(
      'description: Restart the API service and verify health.'
    );
    expect(
      screen.getByRole('heading', { name: 'Restart API' })
    ).toBeInTheDocument();
    expect(screen.getByText('Follow the restart procedure.')).toBeInTheDocument();
  });

  it('does not treat lines that only start with dashes as closing frontmatter delimiters', () => {
    const { container } = render(
      <DocMarkdownPreview
        content={`---
title: Restart API
---not-a-delimiter

# Restart API`}
      />
    );

    expect(container.textContent).toContain('title: Restart API');
    expect(container.textContent).toContain('---not-a-delimiter');
  });
});
