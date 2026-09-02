import { AppBarContext } from '@/contexts/AppBarContext';
import { TOKEN_KEY, useAuth, useIsAdmin } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useUserPreferences } from '@/contexts/UserPreference';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { Terminal } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import {
  base64ToBytes,
  binaryStringToBase64,
  stringToBase64,
} from './encoding';
import { I18nText } from '@/i18n/I18nText';

type MessageType = 'input' | 'output' | 'resize' | 'close' | 'error';
type ConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'error';

function getStatusBadgeClass(status: ConnectionStatus): string {
  switch (status) {
    case 'connected':
      return 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200';
    case 'connecting':
      return 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200';
    case 'error':
      return 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200';
    case 'disconnected':
    default:
      return 'bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200';
  }
}

function getStatusText(
  status: ConnectionStatus,
  errorMessage: string | null
): string {
  switch (status) {
    case 'connected':
      return 'Connected';
    case 'connecting':
      return 'Connecting...';
    case 'error':
      return errorMessage || 'Error';
    case 'disconnected':
    default:
      return 'Disconnected';
  }
}

interface TerminalMessage {
  type: MessageType;
  data?: string;
  cols?: number;
  rows?: number;
}

export default function TerminalPage() {
  const termRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const resizeObserverRef = useRef<ResizeObserver | null>(null);
  const config = useConfig();
  const { user } = useAuth();
  const isAdmin = useIsAdmin();
  const appBarContext = useContext(AppBarContext);
  const [connectionStatus, setConnectionStatus] =
    useState<ConnectionStatus>('disconnected');
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const { preferences } = useUserPreferences();
  const theme = preferences.theme || 'dark';

  // Set page title on mount
  useEffect(() => {
    appBarContext.setTitle('Terminal');
  }, []);

  const sendMessage = useCallback((msg: TerminalMessage) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(msg));
    }
  }, []);

  const handleResize = useCallback(() => {
    if (fitAddonRef.current && terminalRef.current) {
      fitAddonRef.current.fit();
      sendMessage({
        type: 'resize',
        cols: terminalRef.current.cols,
        rows: terminalRef.current.rows,
      });
    }
  }, [sendMessage]);

  useEffect(() => {
    if (!termRef.current || !isAdmin || !config.terminalEnabled) return;

    // Initialize terminal with theme
    const term = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      theme: {
        background: theme === 'dark' ? '#08090e' : '#fbfaf6',
        foreground: theme === 'dark' ? '#dddcd4' : '#14161b',
        cursor: theme === 'dark' ? '#dddcd4' : '#14161b',
        cursorAccent: theme === 'dark' ? '#08090e' : '#fbfaf6',
        selectionBackground: theme === 'dark' ? '#2a2452' : '#d9d3f5',
      },
      allowProposedApi: true,
    });
    const fitAddon = new FitAddon();
    const webLinksAddon = new WebLinksAddon();

    term.loadAddon(fitAddon);
    term.loadAddon(webLinksAddon);
    term.open(termRef.current);

    terminalRef.current = term;
    fitAddonRef.current = fitAddon;
    // Initial fit
    setTimeout(() => fitAddon.fit(), 0);

    // Get token from localStorage
    const token = localStorage.getItem(TOKEN_KEY);
    if (!token) {
      term.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;
      setErrorMessage('Authentication required');
      setConnectionStatus('error');
      return;
    }

    // Build WebSocket URL
    const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${wsProtocol}//${window.location.host}${config.basePath}/api/v1/terminal/ws?token=${encodeURIComponent(token)}`;

    setConnectionStatus('connecting');
    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      setConnectionStatus('connected');
      setErrorMessage(null);
      // Send initial resize
      setTimeout(() => {
        fitAddon.fit();
        ws.send(
          JSON.stringify({
            type: 'resize',
            cols: term.cols,
            rows: term.rows,
          })
        );
      }, 100);
    };

    ws.onmessage = (event) => {
      try {
        const msg: TerminalMessage = JSON.parse(event.data);
        if (msg.type === 'output' && msg.data) {
          term.write(base64ToBytes(msg.data));
        } else if (msg.type === 'error' && msg.data) {
          term.write(`\r\n\x1b[31mError: ${msg.data}\x1b[0m\r\n`);
        }
      } catch (e) {
        console.error('Failed to parse message:', e);
      }
    };

    ws.onerror = () => {
      setConnectionStatus('error');
      setErrorMessage('Connection error');
    };

    ws.onclose = (event) => {
      setConnectionStatus('disconnected');
      if (event.code !== 1000) {
        term.write('\r\n\x1b[33mConnection closed\x1b[0m\r\n');
      }
    };

    // Handle terminal input
    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(
          JSON.stringify({
            type: 'input',
            data: stringToBase64(data),
          })
        );
      }
    });

    term.onBinary((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(
          JSON.stringify({
            type: 'input',
            data: binaryStringToBase64(data),
          })
        );
      }
    });

    // Handle resize with ResizeObserver
    const resizeObserver = new ResizeObserver(() => {
      if (fitAddon && term) {
        fitAddon.fit();
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(
            JSON.stringify({
              type: 'resize',
              cols: term.cols,
              rows: term.rows,
            })
          );
        }
      }
    });
    resizeObserver.observe(termRef.current);
    resizeObserverRef.current = resizeObserver;

    // Cleanup
    return () => {
      resizeObserver.disconnect();
      if (
        ws.readyState === WebSocket.OPEN ||
        ws.readyState === WebSocket.CONNECTING
      ) {
        ws.close(1000, 'Component unmounted');
      }
      term.dispose();
    };
  }, [config.basePath, config.terminalEnabled, isAdmin, theme]);

  // Handle window resize
  useEffect(() => {
    const handleWindowResize = () => handleResize();
    window.addEventListener('resize', handleWindowResize);
    return () => window.removeEventListener('resize', handleWindowResize);
  }, [handleResize]);

  if (!isAdmin) {
    return (
      <div className="flex items-center justify-center h-64">
        <p className="text-muted-foreground">
          <I18nText text={'You do not have permission to access this page.'} />
        </p>
      </div>
    );
  }

  if (!config.terminalEnabled) {
    return (
      <div className="flex items-center justify-center h-64">
        <p className="text-muted-foreground">
          <I18nText
            text={
              'Terminal is not enabled. Set DAGU_TERMINAL_ENABLED=true to enable.'
            }
          />
        </p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full max-w-7xl">
      <div className="flex items-center justify-between mb-2">
        <div>
          <h1 className="text-lg font-semibold">
            <I18nText text={'Terminal'} />
          </h1>
          <p className="text-sm text-muted-foreground">
            <I18nText
              text="Interactive shell connection to local server as {username}"
              values={{ username: user?.username || 'admin' }}
            />
          </p>
        </div>
        <div className="flex items-center gap-2">
          <span
            className={`inline-flex items-center px-2 py-0.5 text-xs rounded ${getStatusBadgeClass(connectionStatus)}`}
          >
            {getStatusText(connectionStatus, errorMessage)}
          </span>
        </div>
      </div>
      {errorMessage && connectionStatus === 'error' && (
        <div className="p-3 mb-2 text-sm text-destructive bg-destructive/10 rounded-md">
          {errorMessage}
        </div>
      )}
      <div
        ref={termRef}
        className="flex-1 rounded border bg-background min-h-0 overflow-hidden"
        style={{ minHeight: '400px' }}
      />
    </div>
  );
}
