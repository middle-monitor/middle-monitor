// Every shape a perimeter view uses to say "I have nothing to show yet".
// Shared: one spec checks these never appear when a view repaints from cache,
// another checks they do appear on a genuine first load.
export const LOADING_SELECTORS = [
  '.loading',
  '.logs-loading',
  '.traces-loading',
  '.network-loading',
  '.incidents-loading',
  '.alert-rules-loading',
  '.profiling-loading',
  '.metrics-chart-loading',
  '.api-keys-loading',
].join(', ');
