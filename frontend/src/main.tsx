import React from 'react'
import ReactDOM from 'react-dom/client'
import { init } from '@middle-monitor/web'
import { QueryClientProvider } from '@tanstack/react-query'
import App from './App.tsx'
import { PUBLIC_API_URL } from './api'
import { AuthProvider } from './contexts/AuthContext'
import { ServiceModalProvider } from './contexts/ServiceModalContext'
import { ThemeProvider } from './contexts/ThemeContext'
import { queryClient } from './queryClient'
import './index.css'
import './i18n';

// Self-monitoring: the dashboard is instrumented with the product's own public
// web SDK, like any customer frontend. Opt-in on the token so a dev boot never
// exports. Before render, so a crash in the first paint is still caught.
const selfMonitorToken = import.meta.env.VITE_MIDDLE_MONITOR_TOKEN ?? '';
if (selfMonitorToken) {
  init({
    apiUrl: PUBLIC_API_URL,
    service: 'middle-front',
    token: selfMonitorToken,
  });
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <AuthProvider>
          <ServiceModalProvider>
            <App />
          </ServiceModalProvider>
        </AuthProvider>
      </ThemeProvider>
    </QueryClientProvider>
  </React.StrictMode>,
)


