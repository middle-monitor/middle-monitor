import { describe, expect, it } from 'vitest';

import { apiErrorMessage } from './apiError';

// A failed write must tell the user why. The message can come from two places,
// and losing either one leaves a form that silently does nothing.
describe('apiErrorMessage', () => {
  it('prefers the error the backend sent', () => {
    const err = { response: { data: { error: 'a host named "web-01" already exists' } } };
    expect(apiErrorMessage(err, 'fallback')).toBe('a host named "web-01" already exists');
  });

  // Demo mode rejects every write client-side, and that rejection carries the
  // only text explaining why nothing happened.
  it('falls back to a client-side rejection message', () => {
    expect(apiErrorMessage(new Error('This demo is read-only.'), 'fallback')).toBe('This demo is read-only.');
  });

  it('uses the caller fallback when neither source has a message', () => {
    expect(apiErrorMessage({}, 'Could not save')).toBe('Could not save');
    expect(apiErrorMessage(null, 'Could not save')).toBe('Could not save');
    expect(apiErrorMessage(undefined, 'Could not save')).toBe('Could not save');
    expect(apiErrorMessage({ response: { data: {} } }, 'Could not save')).toBe('Could not save');
  });

  // An empty string from the backend is not a usable message, so it must not
  // win over the fallback and leave the user with a blank error.
  it('treats an empty backend error as no message', () => {
    const err = { response: { data: { error: '' } }, message: 'Network Error' };
    expect(apiErrorMessage(err, 'fallback')).toBe('Network Error');
  });
});
