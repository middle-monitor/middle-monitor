/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_URL?: string;
  // Opt-in dashboard self-monitoring: the middle-front service token, passed to
  // @middle-monitor/web. Ingest-only — it is public in the bundle. When unset,
  // the SDK is not initialized and nothing is sent.
  readonly VITE_MIDDLE_MONITOR_TOKEN?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
