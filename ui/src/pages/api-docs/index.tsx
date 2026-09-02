// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { AppBarContext } from '@/contexts/AppBarContext';
import { TOKEN_KEY } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import fetchJson from '@/lib/fetchJson';
import {
  AlertCircle,
  ExternalLink,
  RefreshCw,
  ShieldCheck,
} from 'lucide-react';
import * as React from 'react';

const ScalarViewer = React.lazy(() => import('./ScalarViewer'));

type OpenAPIDocument = Record<string, unknown>;

type LoadState =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; spec: OpenAPIDocument };

export default function APIDocsPage(): React.ReactElement {
  const appBarContext = React.useContext(AppBarContext);
  const { selectedRemoteNode, setTitle } = appBarContext;
  const config = useConfig();
  const { ts } = useI18n();
  const [state, setState] = React.useState<LoadState>({ status: 'loading' });
  const requestSeqRef = React.useRef(0);
  const remoteNode = selectedRemoteNode || 'local';
  const openAPIPath = React.useMemo(
    () => `/openapi.json?remoteNode=${encodeURIComponent(remoteNode)}`,
    [remoteNode]
  );

  React.useEffect(() => {
    setTitle('API Docs');
  }, [setTitle]);

  const loadSpec = React.useCallback(async () => {
    const requestSeq = requestSeqRef.current + 1;
    requestSeqRef.current = requestSeq;
    setState({ status: 'loading' });

    try {
      const spec = await fetchJson<OpenAPIDocument>(openAPIPath);
      if (requestSeqRef.current !== requestSeq) {
        return;
      }
      setState({ status: 'ready', spec });
    } catch (error) {
      if (requestSeqRef.current !== requestSeq) {
        return;
      }
      setState({
        status: 'error',
        message:
          error instanceof Error
            ? error.message
            : ts('Failed to load the API reference.'),
      });
    }
  }, [openAPIPath, ts]);

  React.useEffect(() => {
    void loadSpec();
  }, [loadSpec]);

  const preferredBearerToken =
    config.authMode === 'builtin'
      ? (localStorage.getItem(TOKEN_KEY) ?? undefined)
      : undefined;

  return (
    <div className="api-docs-shell flex h-full min-h-0 flex-col gap-4">
      <div className="flex flex-col gap-3 rounded-xl border border-border bg-card/70 p-4 shadow-sm md:flex-row md:items-center md:justify-between">
        <div className="space-y-1">
          <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
            <ShieldCheck className="h-4 w-4 text-primary" />
            <I18nText text="Authenticated reference" />
          </div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            <I18nText text="REST API Docs" />
          </h1>
          <p className="max-w-3xl text-sm text-muted-foreground">
            <I18nText text="The reference is loaded from the live authenticated OpenAPI document served by this Dagu instance." />
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="ghost" onClick={() => void loadSpec()}>
            <RefreshCw className="h-4 w-4" />
            <I18nText text="Reload" />
          </Button>
          <Button variant="outline" asChild>
            <a
              href={`${config.apiURL}${openAPIPath}`}
              target="_blank"
              rel="noreferrer"
            >
              <ExternalLink className="h-4 w-4" />
              <I18nText text="Raw JSON" />
            </a>
          </Button>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden rounded-2xl border border-border bg-background shadow-sm">
        {state.status === 'loading' && (
          <div className="flex h-full min-h-[420px] flex-col items-center justify-center gap-3 px-6 text-center">
            <RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" />
            <div>
              <p className="font-medium text-foreground">
                <I18nText text="Loading API reference" />
              </p>
              <p className="text-sm text-muted-foreground">
                <I18nTemplate
                  text="Fetching {path} with the current auth context."
                  values={{ path: <code>/openapi.json</code> }}
                />
              </p>
            </div>
          </div>
        )}

        {state.status === 'error' && (
          <div className="flex h-full min-h-[420px] flex-col items-center justify-center gap-4 px-6 text-center">
            <AlertCircle className="h-6 w-6 text-destructive" />
            <div className="space-y-1">
              <p className="font-medium text-foreground">
                <I18nText text="Unable to load the API reference" />
              </p>
              <p className="text-sm text-muted-foreground">{state.message}</p>
            </div>
            <Button variant="primary" onClick={() => void loadSpec()}>
              <I18nText text="Try Again" />
            </Button>
          </div>
        )}

        {state.status === 'ready' && (
          <React.Suspense
            fallback={
              <div className="flex h-full min-h-[420px] items-center justify-center">
                <RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" />
              </div>
            }
          >
            <ScalarViewer
              spec={state.spec}
              preferredBearerToken={preferredBearerToken}
            />
          </React.Suspense>
        )}
      </div>
    </div>
  );
}
