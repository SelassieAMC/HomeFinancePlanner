import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './app/App';
// i18next init runs at import time (browser-language default pre-login; the
// logged in user's stored locale takes over in AuthProvider).
import './i18n';
// Self-hosted fonts (no runtime CDN): Instrument Sans carries the UI,
// Spline Sans Mono is reserved for money figures.
import '@fontsource-variable/instrument-sans';
import '@fontsource/spline-sans-mono/400.css';
import '@fontsource/spline-sans-mono/600.css';
import '@fontsource/spline-sans-mono/700.css';
import './styles/global.css';

const rootEl = document.getElementById('root');
if (!rootEl) {
  throw new Error('Root element #root not found');
}

createRoot(rootEl).render(
  <StrictMode>
    <App />
  </StrictMode>,
);