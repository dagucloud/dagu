import { createRoot } from 'react-dom/client';

import { CookiesProvider } from 'react-cookie';
import App from './App';
import '@fontsource-variable/hanken-grotesk';
import './styles/global.css';
import './styles/prism.css';

import { Config } from './contexts/ConfigContext';
import { watchPageVisibility } from './lib/pageVisibility';

declare global {
  const getConfig: () => Config;
}

const container = document.getElementById('root');
const root = createRoot(container!);
const config = getConfig();

watchPageVisibility();

// Clear stale JWT token when not using builtin auth
if (config.authMode !== 'builtin') {
  localStorage.removeItem('dagu_auth_token');
}

root.render(
  <CookiesProvider>
    <App config={config} />
  </CookiesProvider>
);
