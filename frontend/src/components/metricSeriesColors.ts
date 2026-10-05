// Categorical slots in fixed order: a series keeps its colour when a filter
// removes its neighbours. Both columns are stepped for their own surface and
// validated against it, not flipped from one another.
export const SERIES_COLORS_LIGHT = [
  '#2a78d6',
  '#eb6834',
  '#1baf7a',
  '#eda100',
  '#e87ba4',
  '#008300',
  '#4a3aa7',
  '#e34948',
];

export const SERIES_COLORS_DARK = [
  '#3987e5',
  '#d95926',
  '#199e70',
  '#c98500',
  '#d55181',
  '#008300',
  '#9085e9',
  '#e66767',
];

// Past eight lines a chart stops being readable, and the palette has no ninth
// slot: the rest are dropped rather than given a recycled colour.
export const MAX_SERIES = SERIES_COLORS_LIGHT.length;

export function seriesColors(isDark: boolean): string[] {
  return isDark ? SERIES_COLORS_DARK : SERIES_COLORS_LIGHT;
}

/** "key=value, key=value", or the metric name when a series carries no labels. */
export function seriesLabel(labels: Record<string, string>, fallback: string): string {
  const entries = Object.entries(labels);
  if (!entries.length) return fallback;
  return entries.map(([key, value]) => `${key}=${value}`).join(', ');
}
