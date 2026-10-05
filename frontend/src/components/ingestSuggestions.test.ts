import { describe, expect, it } from 'vitest';

import type { TargetIngestCost } from '../api';
import { suggestionsFor } from './ingestSuggestions';

const target = (over: Partial<TargetIngestCost>): TargetIngestCost => ({
  name: 'dns_exporter',
  points_per_minute: 7412,
  series: 1853,
  scrape_interval_seconds: 15,
  bucket_points_per_minute: 6800,
  top_metrics: [],
  ...over,
});

// The suggestion is what the user pastes into the agent config: it must name the
// target, save what it says, and come biggest saving first.
describe('suggestionsFor', () => {
  it('suggests dropping buckets first when they are most of the target', () => {
    const [first, second] = suggestionsFor(target({}));
    expect(first.kind).toBe('drop_buckets');
    expect(first.saves).toBe(6800);
    expect(first.yaml).toBe('- name: dns_exporter\n  drop_metrics:\n    - ".*_bucket"');
    // 15s to 60s keeps a quarter of the points.
    expect(second).toEqual({ kind: 'interval', saves: 5559, yaml: '- name: dns_exporter\n  interval: 60' });
  });

  // A change saving a sliver is not worth editing a config for.
  it('stays quiet when buckets are a small share and the target is light or slow', () => {
    expect(suggestionsFor(target({ bucket_points_per_minute: 100, points_per_minute: 1000, scrape_interval_seconds: 60 }))).toEqual([]);
    expect(suggestionsFor(target({ bucket_points_per_minute: 0, points_per_minute: 300 }))).toEqual([]);
  });

  // The agent's system metrics are not a scrape target: there is no config to change.
  it('never suggests anything for system metrics', () => {
    expect(suggestionsFor(target({ name: '' }))).toEqual([]);
  });
});
