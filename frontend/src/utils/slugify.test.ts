import { describe, expect, it } from 'vitest';

import { slugifyName } from './slugify';

// The slug becomes the technical `name` of a host or service, which reaches
// URLs and the agent's registration payload. Anything outside [a-z0-9-] there
// is a broken identifier, not a cosmetic issue.
describe('slugifyName', () => {
  it('lowercases and hyphenates a human label', () => {
    expect(slugifyName('Web Server 01')).toBe('web-server-01');
  });

  it('drops characters that cannot appear in an identifier', () => {
    expect(slugifyName('API / Gateway (prod)')).toBe('api-gateway-prod');
    expect(slugifyName('café_serveur')).toBe('cafserveur');
  });

  it('collapses runs of hyphens instead of leaving empty segments', () => {
    expect(slugifyName('web   server')).toBe('web-server');
    expect(slugifyName('a -- b')).toBe('a-b');
  });

  it('never leaves a leading or trailing hyphen', () => {
    expect(slugifyName('  spaced  ')).toBe('spaced');
    expect(slugifyName('-edge-')).toBe('edge');
    expect(slugifyName('!!!')).toBe('');
  });

  it('is idempotent, so re-slugifying a slug changes nothing', () => {
    const once = slugifyName('Web Server 01');
    expect(slugifyName(once)).toBe(once);
  });
});
