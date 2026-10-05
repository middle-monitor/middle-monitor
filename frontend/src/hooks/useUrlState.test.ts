import { describe, expect, it } from 'vitest';

import { pageFromUrl, pageToUrl } from './useUrlState';

// The URL is what a colleague receives when a list is shared, so its page
// number is the human one, 1-based, while the views index from 0. Getting the
// offset wrong sends the reader to the wrong page of a shared link.
describe('page numbering in the URL', () => {
  it('shows the first page as 1 and stores it as 0', () => {
    expect(pageToUrl(0)).toBe('1');
    expect(pageFromUrl('1')).toBe(0);
  });

  it('round-trips any page', () => {
    for (const page of [0, 1, 5, 42, 999]) {
      expect(pageFromUrl(pageToUrl(page))).toBe(page);
    }
  });

  // The value comes from the address bar, so it can be anything at all. Every
  // unusable value has to land on the first page rather than on NaN, which
  // would ask the backend for an offset of NaN.
  it('falls back to the first page on anything unusable', () => {
    for (const raw of ['', '0', '-3', 'abc', 'NaN', 'Infinity', '1e400', '  ', 'null']) {
      expect(pageFromUrl(raw)).toBe(0);
    }
  });

  it('truncates a fractional page instead of passing it through', () => {
    expect(pageFromUrl('3.9')).toBe(2);
  });
});
