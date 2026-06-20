// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';
import ArtifactsTab from '../ArtifactsTab';

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
}));

const getMock = vi.fn();
const clipboardWriteTextMock = vi.fn();
const useClientMock = vi.mocked(useClient);

const appBarValue = {
  title: 'DAG Runs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const dagRun = {
  name: 'example-dag',
  dagRunId: 'run-1',
  artifactsAvailable: true,
} as never;

function renderArtifactsTab() {
  return render(
    <AppBarContext.Provider value={appBarValue}>
      <ArtifactsTab dagRun={dagRun} artifactEnabled />
    </AppBarContext.Provider>
  );
}

describe('ArtifactsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    clipboardWriteTextMock.mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: clipboardWriteTextMock,
      },
    });

    useClientMock.mockReturnValue({
      GET: getMock,
    } as never);
  });

  it('lets users switch markdown artifacts between preview and raw modes', async () => {
    getMock.mockImplementation(
      (path: string, init?: { params?: { query?: { path?: string } } }) => {
        if (path === '/dag-runs/{name}/{dagRunId}/artifacts') {
          return Promise.resolve({
            data: {
              items: [
                {
                  name: 'notes.md',
                  path: 'notes.md',
                  type: 'file',
                  size: 32,
                },
              ],
            },
          });
        }

        if (
          path === '/dag-runs/{name}/{dagRunId}/artifacts/preview' &&
          init?.params?.query?.path === 'notes.md'
        ) {
          return Promise.resolve({
            data: {
              name: 'notes.md',
              path: 'notes.md',
              kind: 'markdown',
              mimeType: 'text/markdown',
              size: 32,
              tooLarge: false,
              truncated: false,
              content: '# Heading\n\n**bold** text',
            },
          });
        }

        throw new Error(`Unhandled request: ${path}`);
      }
    );

    const user = userEvent.setup();
    renderArtifactsTab();

    expect(
      await screen.findByRole('button', { name: 'Preview' })
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Raw' })).toBeInTheDocument();
    expect(await screen.findByText('Heading')).toBeInTheDocument();
    expect(
      screen.queryByText((content) => content.includes('**bold** text'))
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Raw' }));

    expect(
      await screen.findByText((content) => content.includes('**bold** text'))
    ).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Preview' }));

    await waitFor(() => {
      expect(
        screen.queryByText((content) => content.includes('**bold** text'))
      ).not.toBeInTheDocument();
    });
  });

  it('copies the full contents of truncated text artifacts', async () => {
    const downloadedArtifact = {
      text: vi.fn().mockResolvedValue('full artifact contents'),
    } as unknown as Blob;

    getMock.mockImplementation(
      (path: string, init?: { params?: { query?: { path?: string } } }) => {
        if (path === '/dag-runs/{name}/{dagRunId}/artifacts') {
          return Promise.resolve({
            data: {
              items: [
                {
                  name: 'output.txt',
                  path: 'output.txt',
                  type: 'file',
                  size: 4096,
                },
              ],
            },
          });
        }

        if (
          path === '/dag-runs/{name}/{dagRunId}/artifacts/preview' &&
          init?.params?.query?.path === 'output.txt'
        ) {
          return Promise.resolve({
            data: {
              name: 'output.txt',
              path: 'output.txt',
              kind: 'text',
              mimeType: 'text/plain',
              size: 4096,
              tooLarge: false,
              truncated: true,
              content: 'partial preview',
            },
          });
        }

        if (
          path === '/dag-runs/{name}/{dagRunId}/artifacts/download' &&
          init?.params?.query?.path === 'output.txt'
        ) {
          return Promise.resolve({
            data: downloadedArtifact,
            response: new Response('full artifact contents'),
          });
        }

        throw new Error(`Unhandled request: ${path}`);
      }
    );

    renderArtifactsTab();

    expect(await screen.findByText('partial preview')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));

    await waitFor(() => {
      expect(clipboardWriteTextMock).toHaveBeenCalledWith(
        'full artifact contents'
      );
    });
  });

  it('renders html artifacts in a sandboxed iframe with raw mode', async () => {
    const htmlContent =
      '<section><h1>Report</h1><a href="/next">Next</a></section>';
    getMock.mockImplementation(
      (path: string, init?: { params?: { query?: { path?: string } } }) => {
        if (path === '/dag-runs/{name}/{dagRunId}/artifacts') {
          return Promise.resolve({
            data: {
              items: [
                {
                  name: 'report.html',
                  path: 'report.html',
                  type: 'file',
                  size: htmlContent.length,
                },
              ],
            },
          });
        }

        if (
          path === '/dag-runs/{name}/{dagRunId}/artifacts/preview' &&
          init?.params?.query?.path === 'report.html'
        ) {
          return Promise.resolve({
            data: {
              name: 'report.html',
              path: 'report.html',
              kind: 'html',
              mimeType: 'text/html',
              size: htmlContent.length,
              tooLarge: false,
              truncated: false,
              content: htmlContent,
            },
          });
        }

        throw new Error(`Unhandled request: ${path}`);
      }
    );

    const user = userEvent.setup();
    renderArtifactsTab();

    const iframe = (await screen.findByTitle(
      'HTML artifact preview'
    )) as HTMLIFrameElement;
    const srcDoc = iframe.srcdoc || iframe.getAttribute('srcdoc') || '';
    expect(iframe).toHaveAttribute('sandbox', '');
    expect(iframe).toHaveAttribute('referrerpolicy', 'no-referrer');
    expect(srcDoc).toContain('<meta http-equiv="Content-Security-Policy"');
    expect(srcDoc.indexOf('Content-Security-Policy')).toBeLessThan(
      srcDoc.indexOf('<h1>Report</h1>')
    );
    expect(srcDoc).toContain('data-dagu-preview-href="/next"');

    await user.click(screen.getByRole('button', { name: 'Raw' }));

    expect(
      screen.queryByTitle('HTML artifact preview')
    ).not.toBeInTheDocument();
    expect(
      await screen.findByText((_, element) =>
        Boolean(
          element?.tagName.toLowerCase() === 'pre' &&
            element.textContent?.includes('<h1>Report</h1>')
        )
      )
    ).toBeInTheDocument();
  });

  it('copies inline html artifact contents', async () => {
    const htmlContent = '<section><strong>Report</strong></section>';
    getMock.mockImplementation(
      (path: string, init?: { params?: { query?: { path?: string } } }) => {
        if (path === '/dag-runs/{name}/{dagRunId}/artifacts') {
          return Promise.resolve({
            data: {
              items: [
                {
                  name: 'report.html',
                  path: 'report.html',
                  type: 'file',
                  size: htmlContent.length,
                },
              ],
            },
          });
        }

        if (
          path === '/dag-runs/{name}/{dagRunId}/artifacts/preview' &&
          init?.params?.query?.path === 'report.html'
        ) {
          return Promise.resolve({
            data: {
              name: 'report.html',
              path: 'report.html',
              kind: 'html',
              mimeType: 'text/html',
              size: htmlContent.length,
              tooLarge: false,
              truncated: false,
              content: htmlContent,
            },
          });
        }

        throw new Error(`Unhandled request: ${path}`);
      }
    );

    renderArtifactsTab();

    expect(
      await screen.findByTitle('HTML artifact preview')
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));

    await waitFor(() => {
      expect(clipboardWriteTextMock).toHaveBeenCalledWith(htmlContent);
    });
  });
});
