/**
 * Detects whether an error / 500 is likely caused by a failing external API call
 * (HTTP client). Used in the Context / Correlation section to show "your user got
 * a 500, but it is because of the call to X".
 *
 * Supported patterns (Go net/http, Node fetch/axios, Python requests, etc.):
 * - URL in the message (Get "https://...", fetch to ..., Connection to foo.bar)
 * - connection refused, no such host, timeout, ECONNREFUSED, ENOTFOUND, ETIMEDOUT
 */

export interface ExternalApiCause {
  /** URL or host involved (e.g. "https://foo.bar" or "foo.bar") */
  target: string;
  /** Short reason (e.g. "connection refused", "timeout", "host not found") */
  reason: string;
  /** Full sentence for the UI */
  summary: string;
}

/**
 * Parse the error message and return an "external API call" cause if detected.
 */
export function parseExternalApiCause(message: string): ExternalApiCause | null {
  if (!message || typeof message !== 'string') return null;
  const m = message.trim();

  // ----- Go net/http: Get "https://...": ... or Post "https://...": ...
  const goMethodUrl = m.match(/(?:Get|Post|Put|Patch|Delete)\s+"(https?:\/\/[^"]+)"/i);
  if (goMethodUrl) {
    const url = goMethodUrl[1];
    const after = m.slice(m.indexOf(goMethodUrl[0]) + goMethodUrl[0].length);
    const reason = inferReasonFromSuffix(after);
    return {
      target: url,
      reason: reason.label,
      summary: `The 500 is likely caused by the call to ${formatTarget(url)}: ${reason.label}.`,
    };
  }

  // ----- Go: "dial tcp ... foo.bar:port: connection refused" ou "lookup foo.bar: no such host"
  const goDial = m.match(/dial tcp (?:(?:[^:]+):(\d+)|[^:]+):\s*(connection refused|i\/o timeout)/i);
  if (goDial) {
    const reason = goDial[2].toLowerCase().includes('timeout') ? 'timeout' : 'connection_refused';
    const hostPart = m.match(/dial tcp ([\w.-]+(?::\d+)?)/i);
    const target = hostPart ? hostPart[1] : 'external service';
    return buildCause(target, reason);
  }

  const goLookup = m.match(/lookup ([\w.-]+):\s*no such host/i);
  if (goLookup) {
    return buildCause(goLookup[1], 'no_such_host');
  }

  // ----- Go: "context deadline exceeded", often after a URL
  const goCtx = m.match(/(?:Get|Post|Put|Patch|Delete)\s+"([^"]+)"[^:]*:\s*context deadline exceeded/i)
    || m.match(/([a-zA-Z0-9][\w.-]*\.[a-zA-Z]{2,})[^.]*context deadline exceeded/i);
  if (goCtx) {
    const target = goCtx[1].startsWith('http') ? goCtx[1] : `https://${goCtx[1]}`;
    return buildCause(target, 'timeout');
  }

  // ----- Node: fetch failed, ECONNREFUSED, ENOTFOUND, ETIMEDOUT (often with a URL in the stack or message)
  const nodeErr = m.match(/(?:ECONNREFUSED|ENOTFOUND|ETIMEDOUT|ECONNRESET)/i);
  if (nodeErr) {
    const reason = nodeErr[0].toUpperCase() === 'ENOTFOUND' ? 'no_such_host' : nodeErr[0].toUpperCase() === 'ETIMEDOUT' ? 'timeout' : 'connection_refused';
    const urlMatch = m.match(/(https?:\/\/[^\s"'<>]+)/);
    const target = urlMatch ? urlMatch[1] : m.match(/(?:fetch|request to|connect to)\s+(?:[\w.-]+\.\w+|\S+)/i)?.[0]?.replace(/^(?:fetch|request to|connect to)\s+/i, '') || 'external API';
    return buildCause(target, reason);
  }

  // ----- Python requests: "ConnectionError: ...", "HTTPSConnectionPool(host='foo.bar'..."
  // host=' has to be consumed as a whole: matching only the '=' leaves the
  // capture starting on the quote, which the character class excludes, so the
  // standard urllib3 message never matched at all.
  const pyConn = m.match(/(?:ConnectionError|Connection refused)[^']*host=['"]?([^'"),\s]+)/i)
    || m.match(/(?:HTTPS?ConnectionPool|Max retries).*host=['"]?([^'"),\s]+)/i);
  if (pyConn) {
    const target = pyConn[1];
    const reason = /timeout|timed out/i.test(m) ? 'timeout' : /refused|ConnectionError/i.test(m) ? 'connection_refused' : 'connection_error';
    return buildCause(target, reason);
  }

  // ----- Bare URL in the message (last resort) + a network-failure keyword
  if (/connection refused|timeout|no such host|econnrefused|enotfound|etimedout|connection error|dial tcp|failed to fetch/i.test(m)) {
    const urlInMsg = m.match(/(https?:\/\/[^\s"'<>)\]]+)/);
    const hostInMsg = m.match(/([a-zA-Z0-9][\w.-]*\.[a-zA-Z]{2,})/);
    const target = urlInMsg ? urlInMsg[1] : hostInMsg ? hostInMsg[1] : null;
    if (target) {
      const reason = /timeout|timed out|etimedout/i.test(m) ? 'timeout'
        : /no such host|enotfound|getaddrinfo/i.test(m) ? 'no_such_host'
        : 'connection_refused';
      return buildCause(target, reason);
    }
  }

  return null;
}

function inferReasonFromSuffix(suffix: string): { label: string } {
  const s = suffix.toLowerCase();
  if (/connection refused|econnrefused/i.test(s)) return { label: 'connection refused' };
  if (/no such host|enotfound|getaddrinfo/i.test(s)) return { label: 'host not found (DNS)' };
  if (/context deadline exceeded|timeout|timed out|etimedout|i\/o timeout/i.test(s)) return { label: 'timeout' };
  if (/eof|connection reset|econnreset/i.test(s)) return { label: 'connection dropped (EOF/reset)' };
  if (/connection refused/i.test(s)) return { label: 'connection refused' };
  return { label: 'call failed' };
}

const REASON_LABELS: Record<string, string> = {
  connection_refused: 'connection refused',
  no_such_host: 'host not found (DNS)',
  timeout: 'timeout',
  connection_error: 'connection error',
};

function buildCause(target: string, reasonKey: string): ExternalApiCause {
  const reason = REASON_LABELS[reasonKey] || reasonKey;
  return {
    target,
    reason,
    summary: `The 500 is likely caused by the call to ${formatTarget(target)}: ${reason}.`,
  };
}

function formatTarget(target: string): string {
  try {
    if (target.startsWith('http://') || target.startsWith('https://')) {
      const u = new URL(target);
      return u.origin;
    }
    return target;
  } catch {
    return target;
  }
}
