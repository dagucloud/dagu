import { createRequire } from 'node:module';
import { describe, expect, it } from 'vitest';

const require = createRequire(import.meta.url);
const webpackDevConfig = require('../../webpack.dev.js');

function proxyContexts(config: {
  devServer?: {
    proxy?: Array<{
      context?: string[];
    }>;
  };
}): string[] {
  return (config.devServer?.proxy ?? []).flatMap(
    (entry) => entry.context ?? []
  );
}

describe('webpack dev server proxy', () => {
  it('serves the SPA for dotted client-side routes', () => {
    expect(webpackDevConfig.devServer.historyApiFallback).toEqual({
      disableDotRule: true,
    });
  });

  it('proxies schema asset requests to the backend server', () => {
    expect(proxyContexts(webpackDevConfig)).toEqual(
      expect.arrayContaining([
        '/assets/dag.schema.json',
        '/assets/config.schema.json',
      ])
    );
  });
});
