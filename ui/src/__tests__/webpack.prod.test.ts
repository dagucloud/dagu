import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const webpackProdConfigSource = readFileSync(
  resolve(__dirname, '../../webpack.prod.js'),
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
});
