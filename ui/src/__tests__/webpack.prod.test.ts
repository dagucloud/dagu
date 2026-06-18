// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const webpackProdConfigSource = readFileSync(
  resolve(__dirname, '../../webpack.prod.js'),
  'utf8'
);
const webpackCommonConfigSource = readFileSync(
  resolve(__dirname, '../../webpack.common.js'),
  'utf8'
);

describe('webpack production assets', () => {
  it('uses stable entry and content-hashed lazy chunk filenames', () => {
    expect(webpackProdConfigSource).toContain("filename: 'bundle.js'");
    expect(webpackProdConfigSource).toContain(
      "chunkFilename: '[name].[contenthash:16].bundle.js'"
    );
    expect(webpackProdConfigSource).not.toContain('bundle.js?v=0.0.0');
  });

  it('uses content-hashed Monaco worker filenames', () => {
    expect(webpackCommonConfigSource).toContain(
      "filename: '[name].[contenthash:16].worker.js'"
    );
  });
});
