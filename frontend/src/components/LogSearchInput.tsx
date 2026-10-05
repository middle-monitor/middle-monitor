import { useState, useEffect, useRef, useCallback } from 'react';
import { useTranslation } from 'react-i18next';

const DEBOUNCE_MS = 200;

// d-style facets first, then the raw index fields.
const LOG_FIELDS = [
  'service',
  'status',
  'host',
  'service_name',
  'severity_text',
  'severity',
  'hostname',
  'trace_id',
  'span_id',
  'attributes.',
  'body',
];

export interface LogSearchInputProps {
  value: string;
  onChange: (value: string) => void;
  onSubmit: () => void;
  onFieldValues?: (field: string, prefix: string) => Promise<string[]>;
  placeholder?: string;
  className?: string;
}

function getSeverityValueClass(val: string): string {
  const v = val.toUpperCase();
  if (v === 'ERROR' || v === 'FATAL') return 'severity-error';
  if (v === 'WARN' || v === 'WARNING') return 'severity-warn';
  if (v === 'INFO') return 'severity-info';
  if (v === 'DEBUG') return 'severity-debug';
  return '';
}

function getCurrentToken(
  value: string,
  cursorPos: number,
): { token: string; start: number; end: number } {
  const before = value.slice(0, cursorPos);
  const after = value.slice(cursorPos);
  const start = before.search(/\S+$/);
  const end = cursorPos + (after.match(/^\S*/)?.[0]?.length ?? 0);
  let tokenStart = start >= 0 ? start : cursorPos;
  let token = value.slice(tokenStart, end).trim();
  // Skip d operator prefixes (-error, (service:...) so autocomplete
  // still works right after an exclusion or an opening parenthesis.
  const opPrefix = token.match(/^[-(]+/)?.[0] ?? '';
  tokenStart += opPrefix.length;
  token = token.slice(opPrefix.length);
  return { token, start: tokenStart, end };
}

export function LogSearchInput({
  value,
  onChange,
  onSubmit,
  onFieldValues,
  placeholder,
  className = '',
}: LogSearchInputProps) {
  const { t } = useTranslation();
  const resolvedPlaceholder = placeholder ?? t('log_search.placeholder');
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const [suggestionType, setSuggestionType] = useState<'field' | 'value'>(
    'field',
  );
  // -1 = no suggestion highlighted: Enter searches; arrows highlight first (d behavior).
  const [selectedIndex, setSelectedIndex] = useState(-1);
  const [loading, setLoading] = useState(false);
  const [cursorPos, setCursorPos] = useState(0);
  const [isFocused, setIsFocused] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const fetchTimeoutRef = useRef<ReturnType<typeof setTimeout>>();

  const fetchValues = useCallback(
    async (field: string, prefix: string) => {
      if (!onFieldValues) return [];
      setLoading(true);
      try {
        const values = await onFieldValues(field, prefix);
        return values;
      } finally {
        setLoading(false);
      }
    },
    [onFieldValues],
  );

  useEffect(() => {
    if (!isFocused) {
      setShowSuggestions(false);
      return;
    }

    const pos = cursorPos;
    const tokenInfo = getCurrentToken(value, pos);
    const { token } = tokenInfo;

    if (!token) {
      setSuggestions(LOG_FIELDS);
      setSuggestionType('field');
      setShowSuggestions(true);
      setSelectedIndex(-1);
      return;
    }

    const colonIdx = token.indexOf(':');
    if (colonIdx >= 0) {
      const field = token.slice(0, colonIdx).trim();
      const valuePrefix = token.slice(colonIdx + 1).trim();
      const canFetchValues =
        field !== 'body' &&
        field !== 'attributes.' &&
        (LOG_FIELDS.includes(field) || field.startsWith('attributes.'));
      if (canFetchValues) {
        setSuggestionType('value');
        if (fetchTimeoutRef.current) clearTimeout(fetchTimeoutRef.current);
        fetchTimeoutRef.current = setTimeout(() => {
          fetchValues(field, valuePrefix).then((vals) => {
            setSuggestions(vals);
            setShowSuggestions(true);
            setSelectedIndex(-1);
          });
        }, DEBOUNCE_MS);
      } else {
        setShowSuggestions(false);
      }
    } else {
      const filtered = LOG_FIELDS.filter((f) =>
        f.toLowerCase().includes(token.toLowerCase()),
      );
      setSuggestions(filtered);
      setSuggestionType('field');
      setShowSuggestions(filtered.length > 0);
      setSelectedIndex(-1);
    }
    return () => {
      if (fetchTimeoutRef.current) clearTimeout(fetchTimeoutRef.current);
    };
  }, [value, cursorPos, isFocused, fetchValues]);

  const handleSelect = (suggestion: string) => {
    const pos = inputRef.current?.selectionStart ?? value.length;
    const { token, start, end } = getCurrentToken(value, pos);
    const colonIdx = token.indexOf(':');

    let newValue: string;
    let newCursor: number;

    if (colonIdx >= 0 && suggestionType === 'value') {
      const field = token.slice(0, colonIdx).trim();
      newValue =
        value.slice(0, start) + `${field}:${suggestion} ` + value.slice(end);
      newCursor = start + field.length + 1 + suggestion.length + 1;
    } else if (suggestion === 'attributes.') {
      newValue = value.slice(0, start) + 'attributes.' + value.slice(end);
      newCursor = start + 'attributes.'.length;
    } else {
      newValue = value.slice(0, start) + suggestion + ':' + value.slice(end);
      newCursor = start + suggestion.length + 1;
    }

    onChange(newValue);
    setShowSuggestions(false);
    setTimeout(() => {
      inputRef.current?.focus();
      inputRef.current?.setSelectionRange(newCursor, newCursor);
    }, 0);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (!showSuggestions || suggestions.length === 0) {
      if (e.key === 'Enter') onSubmit();
      return;
    }

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSelectedIndex((i) => (i + 1) % suggestions.length);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSelectedIndex((i) => (i <= 0 ? suggestions.length - 1 : i - 1));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      // Enter completes only an arrow-highlighted suggestion; otherwise it searches.
      if (selectedIndex >= 0 && suggestions[selectedIndex]) {
        handleSelect(suggestions[selectedIndex]);
      } else {
        setShowSuggestions(false);
        onSubmit();
      }
    } else if (e.key === 'Escape') {
      setShowSuggestions(false);
    }
  };

  return (
    <div className={`log-search-input-wrapper ${className}`}>
      <input
        ref={inputRef}
        type='text'
        className='log-search-input'
        value={value}
        onChange={(e) => {
          setCursorPos(e.target.selectionStart ?? e.target.value.length);
          onChange(e.target.value);
        }}
        onFocus={() => {
          setIsFocused(true);
          setCursorPos(inputRef.current?.selectionStart ?? value.length);
        }}
        onBlur={() =>
          setTimeout(() => {
            setIsFocused(false);
            setShowSuggestions(false);
          }, 150)
        }
        onKeyDown={handleKeyDown}
        onClick={() => setCursorPos(inputRef.current?.selectionStart ?? 0)}
        placeholder={resolvedPlaceholder}
      />
      {showSuggestions && (suggestions.length > 0 || loading) && (
        <div ref={dropdownRef} className='log-search-suggestions'>
          {loading ? (
            <div className='log-search-suggestion-item'>
              {t('common.loading')}
            </div>
          ) : (
            suggestions.map((s, i) => (
              <div
                key={s}
                className={`log-search-suggestion-item ${i === selectedIndex ? 'selected' : ''}`}
                onMouseDown={(e) => {
                  e.preventDefault();
                  handleSelect(s);
                }}>
                {suggestionType === 'field' ? (
                  <>
                    <span className='log-search-field'>{s}</span>
                    <span className='log-search-hint'>:</span>
                  </>
                ) : (
                  <span
                    className={`log-search-value ${getSeverityValueClass(s)}`}>
                    {s}
                  </span>
                )}
              </div>
            ))
          )}
        </div>
      )}
    </div>
  );
}
