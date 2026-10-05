import { describe, expect, it } from 'vitest';

import { parseExternalApiCause } from './externalApiCause';

// This parser is what turns "your users got a 500" into "your users got a 500
// because the call to Stripe timed out". It reads error text produced by four
// different runtimes, so each shape it claims to support is worth pinning: a
// regex that quietly stops matching downgrades the correlation panel to silence
// rather than to a visible error.
describe('parseExternalApiCause', () => {
  it('reads a Go net/http verb and URL', () => {
    const cause = parseExternalApiCause('Get "https://api.stripe.com/v1/charges": dial tcp: connection refused');
    expect(cause?.target).toBe('https://api.stripe.com/v1/charges');
    expect(cause?.reason).toBe('connection refused');
    // The summary shows the origin, not the full path, so a secret in a query
    // string never reaches the panel.
    expect(cause?.summary).toContain('https://api.stripe.com');
    expect(cause?.summary).not.toContain('/v1/charges');
  });

  it('reads a Go dial failure', () => {
    const cause = parseExternalApiCause('dial tcp 10.0.0.5:5432: connection refused');
    expect(cause?.target).toBe('10.0.0.5:5432');
    expect(cause?.reason).toBe('connection refused');
  });

  it('reads a Go DNS failure', () => {
    const cause = parseExternalApiCause('lookup payments.internal: no such host');
    expect(cause?.target).toBe('payments.internal');
    expect(cause?.reason).toBe('host not found (DNS)');
  });

  it('reads a Go context deadline as a timeout', () => {
    const cause = parseExternalApiCause('Get "https://slow.example.com/x": context deadline exceeded');
    expect(cause?.reason).toBe('timeout');
  });

  it('reads Node error codes', () => {
    expect(parseExternalApiCause('connect ECONNREFUSED 127.0.0.1:6379')?.reason).toBe('connection refused');
    expect(parseExternalApiCause('getaddrinfo ENOTFOUND api.example.com')?.reason).toBe('host not found (DNS)');
    expect(parseExternalApiCause('connect ETIMEDOUT 1.2.3.4:443')?.reason).toBe('timeout');
  });

  it('reads a Python requests connection pool failure', () => {
    const cause = parseExternalApiCause(
      "HTTPSConnectionPool(host='api.example.com', port=443): Max retries exceeded"
    );
    expect(cause?.target).toBe('api.example.com');
  });

  // An application error that has nothing to do with an outbound call must not
  // be dressed up as one, or the panel points the reader at the wrong system.
  it('returns null for errors that are not a failed outbound call', () => {
    expect(parseExternalApiCause('TypeError: cannot read property id of undefined')).toBeNull();
    expect(parseExternalApiCause('division by zero')).toBeNull();
    expect(parseExternalApiCause('')).toBeNull();
    // A bare hostname with no failure keyword is not evidence of anything.
    expect(parseExternalApiCause('processed order for api.example.com')).toBeNull();
  });

  // The message comes from stored error rows, so a non-string must not throw
  // and blank the panel.
  it('survives a non-string message', () => {
    expect(parseExternalApiCause(null as unknown as string)).toBeNull();
    expect(parseExternalApiCause(42 as unknown as string)).toBeNull();
  });
});
