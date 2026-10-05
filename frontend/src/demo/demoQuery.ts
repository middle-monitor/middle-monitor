import type { LogEntry, TraceSpan } from '../api';

/**
 * Datadog-style query evaluator for the demo client. Mirrors what the backend
 * hands to OpenSearch query_string (see backend/services/searchquery.go):
 * implicit AND between terms, AND/&&, OR/||, NOT/- exclusion, parentheses,
 * quoted phrases, field:value facets (with the service/status/host aliases)
 * and * wildcards. A malformed query matches nothing, like the real search
 * (OpenSearch answers 400 and the API returns zero hits).
 *
 * Logs and traces share the grammar: a query typed in one view has to behave
 * the same in the other.
 */

type Pred<T> = (e: T) => boolean;

interface QuerySpec<T> {
  aliases: Record<string, string>;
  /** Names a user may type before a colon, aliases included. */
  facets: string[];
  /** Resolved field name to its value on the entry. */
  fieldValue: (e: T, field: string) => string | undefined;
  /** What a bare term is matched against. */
  freeText: (e: T) => string;
  /** Fields with full-text (substring) semantics; the others are exact. */
  fullText: string[];
}

type Token =
  | { kind: 'lparen' }
  | { kind: 'rparen' }
  | { kind: 'and' }
  | { kind: 'or' }
  | { kind: 'not' }
  | { kind: 'term'; field?: string; value: string; quoted: boolean };

class QuerySyntaxError extends Error {}

function tokenize(q: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;

  const readQuoted = (): string => {
    i++; // opening quote
    const start = i;
    while (i < q.length && q[i] !== '"') i++;
    if (i >= q.length) throw new QuerySyntaxError('unclosed quote');
    const phrase = q.slice(start, i);
    i++; // closing quote
    return phrase;
  };

  while (i < q.length) {
    const c = q[i];
    if (c === ' ' || c === '\t') {
      i++;
    } else if (c === '(') {
      tokens.push({ kind: 'lparen' });
      i++;
    } else if (c === ')') {
      tokens.push({ kind: 'rparen' });
      i++;
    } else if (c === '-') {
      tokens.push({ kind: 'not' });
      i++;
    } else if (c === '&' && q[i + 1] === '&') {
      tokens.push({ kind: 'and' });
      i += 2;
    } else if (c === '|' && q[i + 1] === '|') {
      tokens.push({ kind: 'or' });
      i += 2;
    } else if (c === '"') {
      tokens.push({ kind: 'term', value: readQuoted(), quoted: true });
    } else {
      let word = '';
      while (i < q.length && !' \t()'.includes(q[i]) && q[i] !== '"') {
        word += q[i];
        i++;
        // field:"quoted value"
        if (word.endsWith(':') && q[i] === '"') {
          tokens.push({
            kind: 'term',
            field: word.slice(0, -1),
            value: readQuoted(),
            quoted: true,
          });
          word = '';
          break;
        }
      }
      if (!word) continue;
      if (word === 'AND') tokens.push({ kind: 'and' });
      else if (word === 'OR') tokens.push({ kind: 'or' });
      else if (word === 'NOT') tokens.push({ kind: 'not' });
      else {
        const colonIdx = word.indexOf(':');
        if (colonIdx > 0) {
          tokens.push({
            kind: 'term',
            field: word.slice(0, colonIdx),
            value: word.slice(colonIdx + 1),
            quoted: false,
          });
        } else {
          tokens.push({ kind: 'term', value: word, quoted: false });
        }
      }
    }
  }
  return tokens;
}

function wildcardRegex(value: string, anchored: boolean): RegExp {
  const escaped = value
    .replace(/[.+?^${}()|[\]\\]/g, '\\$&')
    .replace(/\*/g, '.*');
  return new RegExp(anchored ? `^${escaped}$` : escaped, 'i');
}

// A space after the colon is what people actually type. Lucene reads
// "service_name: api" as an empty field term and matches nothing, so the
// backend closes the gap for known facet names only: free text like
// "timeout: refused" keeps its colon. Quoted phrases are user text.
function collapseFacetSpaces(q: string, facets: string[]): string {
  const re = new RegExp(`\\b(${facets.join('|')}):[ \\t]+`, 'g');
  return q
    .split('"')
    .map((seg, i) => (i % 2 === 0 ? seg.replace(re, '$1:') : seg))
    .join('"');
}

function termPred<T>(
  spec: QuerySpec<T>,
  field: string | undefined,
  value: string,
  quoted: boolean,
): Pred<T> {
  if (field !== undefined) {
    const resolved = spec.aliases[field] ?? field;
    const hasWildcard = !quoted && value.includes('*');
    const isFullText = spec.fullText.includes(resolved);
    return (e) => {
      const actual = spec.fieldValue(e, resolved);
      if (actual === undefined || value === '') return false;
      if (isFullText) {
        return hasWildcard
          ? wildcardRegex(value, false).test(actual)
          : actual.toLowerCase().includes(value.toLowerCase());
      }
      // keyword fields: exact match, wildcards allowed
      return hasWildcard
        ? wildcardRegex(value, true).test(actual)
        : actual.toLowerCase() === value.toLowerCase();
    };
  }
  // Free text searches the same fields as the backend query_string clause.
  const hasWildcard = !quoted && value.includes('*');
  const re = hasWildcard ? wildcardRegex(value, false) : null;
  const needle = value.toLowerCase();
  return (e) => {
    const haystack = spec.freeText(e);
    return re ? re.test(haystack) : haystack.toLowerCase().includes(needle);
  };
}

// Grammar: or := and (OR and)* ; and := unary ((AND)? unary)* ; unary := NOT unary | primary
function parse<T>(tokens: Token[], spec: QuerySpec<T>): Pred<T> {
  let pos = 0;
  const peek = () => tokens[pos];

  const parsePrimary = (): Pred<T> => {
    const tok = peek();
    if (!tok) throw new QuerySyntaxError('unexpected end of query');
    if (tok.kind === 'lparen') {
      pos++;
      const inner = parseOr();
      if (peek()?.kind !== 'rparen')
        throw new QuerySyntaxError('missing closing parenthesis');
      pos++;
      return inner;
    }
    if (tok.kind === 'term') {
      pos++;
      return termPred(spec, tok.field, tok.value, tok.quoted);
    }
    throw new QuerySyntaxError(`unexpected token ${tok.kind}`);
  };

  const parseUnary = (): Pred<T> => {
    if (peek()?.kind === 'not') {
      pos++;
      const inner = parseUnary();
      return (e) => !inner(e);
    }
    return parsePrimary();
  };

  const parseAnd = (): Pred<T> => {
    const parts: Pred<T>[] = [parseUnary()];
    for (;;) {
      const tok = peek();
      if (tok?.kind === 'and') {
        pos++;
        parts.push(parseUnary());
      } else if (
        tok &&
        (tok.kind === 'term' || tok.kind === 'lparen' || tok.kind === 'not')
      ) {
        parts.push(parseUnary()); // implicit AND on juxtaposition
      } else {
        break;
      }
    }
    return (e) => parts.every((p) => p(e));
  };

  const parseOr = (): Pred<T> => {
    const parts: Pred<T>[] = [parseAnd()];
    while (peek()?.kind === 'or') {
      pos++;
      parts.push(parseAnd());
    }
    return (e) => parts.some((p) => p(e));
  };

  const result = parseOr();
  if (pos < tokens.length) throw new QuerySyntaxError('trailing tokens');
  return result;
}

function compile<T>(q: string, spec: QuerySpec<T>): Pred<T> {
  const trimmed = q.trim();
  if (!trimmed) return () => true;
  try {
    return parse(tokenize(collapseFacetSpaces(trimmed, spec.facets)), spec);
  } catch (err) {
    if (err instanceof QuerySyntaxError) return () => false;
    throw err;
  }
}

// Free text reaches trace_id and span_id on both signals: pasting an id read
// off a detail panel is the fastest way to correlate a log with its trace.
const LOG_SPEC: QuerySpec<LogEntry> = {
  aliases: {
    service: 'service_name',
    status: 'severity_text',
    severity: 'severity_text',
    host: 'hostname',
  },
  facets: [
    'service',
    'status',
    'severity',
    'host',
    'service_name',
    'severity_text',
    'hostname',
    'body',
    'trace_id',
    'span_id',
  ],
  fieldValue: (e, field) => {
    switch (field) {
      case 'service_name':
        return e.service_name;
      case 'severity_text':
        return e.severity_text;
      case 'hostname':
        return e.hostname;
      case 'trace_id':
        return e.trace_id;
      case 'span_id':
        return e.span_id;
      case 'body':
        return e.body;
      default:
        return undefined; // unknown facet (e.g. attributes.*): no demo log has it
    }
  },
  freeText: (e) =>
    `${e.body} ${e.service_name} ${e.severity_text} ${e.hostname} ${e.trace_id ?? ''} ${e.span_id ?? ''}`,
  fullText: ['body'],
};

const TRACE_SPEC: QuerySpec<TraceSpan> = {
  aliases: {
    service: 'service_name',
    operation: 'operation_name',
    host: 'hostname',
    status: 'status_code',
    kind: 'span_kind',
  },
  facets: [
    'service',
    'operation',
    'host',
    'status',
    'kind',
    'service_name',
    'operation_name',
    'span_kind',
    'status_code',
    'hostname',
    'trace_id',
    'span_id',
    'parent_span_id',
  ],
  fieldValue: (e, field) => {
    switch (field) {
      case 'service_name':
        return e.service_name;
      case 'operation_name':
        return e.operation_name;
      case 'span_kind':
        return e.span_kind;
      case 'status_code':
        return e.status_code;
      case 'hostname':
        return e.hostname;
      case 'trace_id':
        return e.trace_id;
      case 'span_id':
        return e.span_id;
      case 'parent_span_id':
        return e.parent_span_id;
      default:
        return undefined;
    }
  },
  freeText: (e) =>
    `${e.operation_name} ${e.service_name} ${e.span_kind} ${e.status_code} ${e.hostname ?? ''} ${e.trace_id} ${e.span_id} ${e.parent_span_id ?? ''}`,
  fullText: ['operation_name'],
};

export function compileLogQuery(q: string): Pred<LogEntry> {
  return compile(q, LOG_SPEC);
}

export function compileTraceQuery(q: string): Pred<TraceSpan> {
  return compile(q, TRACE_SPEC);
}
