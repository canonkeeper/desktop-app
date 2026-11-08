import './app/zod-config';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { createCanonKeeperApiClient } from './shared/api';
import { AppProviders, createCanonKeeperQueryClient } from './app/AppProviders';
import { createClientErrorReporter, installClientErrorReporting, setClientErrorReporter } from './app/client-errors';
import { CanonKeeperRouterProvider, createCanonKeeperRouter } from './routing/router';
import './styles/fonts.css';
import './styles/tokens.css';
import './styles/reading-themes.css';
import './styles/base.css';

const root = document.getElementById('root');

if (!root) {
  throw new Error('Root element #root was not found.');
}

const api = createCanonKeeperApiClient();
const clientErrors = createClientErrorReporter(api);
setClientErrorReporter(clientErrors);
installClientErrorReporting(clientErrors);
const queryClient = createCanonKeeperQueryClient();
const router = createCanonKeeperRouter({ api, queryClient });

createRoot(root).render(
  <StrictMode>
    <AppProviders queryClient={queryClient}>
      <CanonKeeperRouterProvider router={router} />
    </AppProviders>
  </StrictMode>,
);
