import type { TargetIngestCost } from '../api';

// A suggestion is worth showing only when it saves a noticeable part of the
// target: below that the user would edit their config for nothing.
const MIN_BUCKET_SHARE = 0.2;
const MIN_POINTS_FOR_INTERVAL = 500;
const SUGGESTED_INTERVAL = 60;

export interface IngestSuggestion {
  kind: 'drop_buckets' | 'interval';
  saves: number;
  yaml: string;
}

/** The config changes worth making on one target, biggest saving first. */
export function suggestionsFor(target: TargetIngestCost): IngestSuggestion[] {
  if (!target.name) return [];
  const out: IngestSuggestion[] = [];
  if (target.bucket_points_per_minute >= target.points_per_minute * MIN_BUCKET_SHARE && target.bucket_points_per_minute > 0) {
    out.push({
      kind: 'drop_buckets',
      saves: target.bucket_points_per_minute,
      yaml: `- name: ${target.name}\n  drop_metrics:\n    - ".*_bucket"`,
    });
  }
  const interval = target.scrape_interval_seconds;
  if (interval > 0 && interval < SUGGESTED_INTERVAL && target.points_per_minute >= MIN_POINTS_FOR_INTERVAL) {
    out.push({
      kind: 'interval',
      saves: Math.round(target.points_per_minute * (1 - interval / SUGGESTED_INTERVAL)),
      yaml: `- name: ${target.name}\n  interval: ${SUGGESTED_INTERVAL}`,
    });
  }
  return out.sort((a, b) => b.saves - a.saves);
}
