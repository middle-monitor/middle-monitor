import React, { useState, useEffect, useRef } from 'react';
import { HiSparkles, HiRefresh, HiExclamationCircle } from 'react-icons/hi';
import { useTranslation } from 'react-i18next';
import './ExplainPanel.css';

interface ExplanationMetadata {
  subject_type: string;
  subject_id: number;
  cached: boolean;
  model?: string;
  duration_ms?: number;
  created_at: string;
}

interface ExplainPanelProps {
  fetchExplanationSSE: (force: boolean) => AsyncGenerator<{ type: 'metadata' | 'chunk' | 'error', data: any }>;
  label?: string;
  autoRun?: boolean;
}

/**
 * Renders the root-cause analysis, streamed over SSE. Non-obvious causes go
 * through an LLM (EXPLAIN_MODE=single_shot); well-known failure signatures are
 * answered instantly by the deterministic engine.
 */
export function ExplainPanel({ fetchExplanationSSE, label, autoRun }: ExplainPanelProps) {
  const { t } = useTranslation();
  const resolvedLabel = label ?? t('explain_panel.default_label');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [content, setContent] = useState<string>('');
  const [meta, setMeta] = useState<ExplanationMetadata | null>(null);
  const [hasStarted, setHasStarted] = useState(false);
  const [isTyping, setIsTyping] = useState(false);
  const autoRunFired = useRef(false);

  const run = async (force: boolean) => {
    setLoading(true);
    setError(null);
    setContent('');
    setMeta(null);
    setHasStarted(true);
    setIsTyping(true);

    try {
      const generator = fetchExplanationSSE(force);
      for await (const event of generator) {
        if (event.type === 'metadata') {
          setMeta(event.data);
        } else if (event.type === 'chunk') {
          // Accumulate raw: normalizing here trims the accumulated text on every
          // chunk, so a chunk ending on a space lost it and the next one glued
          // onto the previous word ("91.39%on worker-01").
          setContent((prev) => prev + String(event.data));
        } else if (event.type === 'error') {
          throw new Error(event.data);
        }
      }
    } catch (e: any) {
      setError(e.message || t('explain_panel.generation_failed'));
    } finally {
      setLoading(false);
      setIsTyping(false);
    }
  };

  // Auto-start on mount when the parent requests it (e.g. clicking "analyze cause").
  useEffect(() => {
    if (autoRun && !autoRunFired.current) {
      autoRunFired.current = true;
      run(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoRun]);

  if (!hasStarted && !loading && !error && !content) {
    return (
      <div className="explain-panel-pro explain-panel-empty">
        <div className="explain-panel-empty-copy">
          <div className="explain-panel-empty-icon">
            <HiSparkles size={16} />
          </div>
          <span>{t('explain_panel.empty_description')}</span>
        </div>
        <button type="button" className="btn btn-primary explain-panel-action" onClick={() => run(false)}>
          <HiSparkles /> <span>{resolvedLabel}</span>
        </button>
      </div>
    );
  }

  // The engine names itself: only an answer that came from a model is labelled AI.
  const ruleBased = meta?.model === 'rules' || meta?.model === 'deterministic';

  return (
    <div className="explain-panel-pro">
      <div className="explain-panel-header">
        <div className="explain-panel-title">
          {t('explain_panel.title')}
          {meta?.model && (
            <span className="explain-panel-ai-badge">
              {ruleBased ? (
                t('explain_panel.rules_badge')
              ) : (
                <>
                  <HiSparkles size={12} /> {t('explain_panel.ai_badge')}
                </>
              )}
            </span>
          )}
        </div>
        <div className="explain-panel-meta">
          {meta?.cached && <span className="explain-panel-badge">{t('explain_panel.cached_badge')}</span>}
          {meta?.model && !ruleBased && <span style={{ color: 'rgba(255,255,255,0.5)' }}>{meta.model}</span>}
          {meta?.duration_ms && <span style={{ color: 'rgba(255,255,255,0.3)' }}>{meta.duration_ms}ms</span>}
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => run(true)}
            disabled={loading}
            title={t('explain_panel.regenerate_title')}>
            <HiRefresh /> <span>{t('explain_panel.regenerate')}</span>
          </button>
        </div>
      </div>

      {loading && !content && (
        <div className="explain-skeleton">
          <div className="explain-skeleton-line"></div>
          <div className="explain-skeleton-line"></div>
          <div className="explain-skeleton-line"></div>
          <div className="explain-skeleton-line"></div>
        </div>
      )}

      {error && !loading && (
        <div style={{ color: 'var(--status-error)', display: 'flex', alignItems: 'center', gap: '0.5rem', padding: '0.5rem 0' }}>
          <HiExclamationCircle size={18} /> {error}
        </div>
      )}

      {content && (
        <div className="explain-panel-content">
          <MarkdownBlock source={content} />
          {isTyping && <span className="typewriter-cursor"></span>}
        </div>
      )}
    </div>
  );
}

/**
 * Ultra-light markdown renderer for the well-defined recipe output shape
 */
function MarkdownBlock({ source }: { source: string }) {
  const lines = normalizeExplanation(source).split(/\r?\n/);
  type Block =
    | { kind: 'h2'; text: string }
    | { kind: 'h3'; text: string }
    | { kind: 'p'; text: string }
    | { kind: 'ul'; items: string[] }
    | { kind: 'ol'; items: string[] };
  const blocks: Block[] = [];
  let buf: string[] = [];
  const flushParagraph = () => {
    if (buf.length) {
      blocks.push({ kind: 'p', text: buf.join(' ') });
      buf = [];
    }
  };

  for (const raw of lines) {
    const line = raw.trim();
    if (line === '') {
      flushParagraph();
      continue;
    }
    if (line.startsWith('## ')) {
      flushParagraph();
      blocks.push({ kind: 'h2', text: line.slice(3) });
      continue;
    }
    if (line.startsWith('### ')) {
      flushParagraph();
      blocks.push({ kind: 'h3', text: line.slice(4) });
      continue;
    }
    if (/^-\s+/.test(line)) {
      flushParagraph();
      const last = blocks[blocks.length - 1];
      const item = line.replace(/^-\s+/, '');
      if (last && last.kind === 'ul') last.items.push(item);
      else blocks.push({ kind: 'ul', items: [item] });
      continue;
    }
    if (/^\d+\.\s+/.test(line)) {
      flushParagraph();
      const last = blocks[blocks.length - 1];
      const item = line.replace(/^\d+\.\s+/, '');
      if (last && last.kind === 'ol') last.items.push(item);
      else blocks.push({ kind: 'ol', items: [item] });
      continue;
    }
    buf.push(line);
  }
  flushParagraph();

  return (
    <>
      {blocks.map((b, i) => {
        switch (b.kind) {
          case 'h2':
            return <h2 key={i}>{b.text}</h2>;
          case 'h3':
            return <h3 key={i}>{b.text}</h3>;
          case 'p':
            return <p key={i}>{inlineFormat(b.text)}</p>;
          case 'ul':
            return (
              <ul key={i}>
                {b.items.map((it, j) => (
                  <li key={j}>{inlineFormat(it)}</li>
                ))}
              </ul>
            );
          case 'ol':
            return (
              <ol key={i}>
                {b.items.map((it, j) => (
                  <li key={j}>{inlineFormat(it)}</li>
                ))}
              </ol>
            );
        }
      })}
    </>
  );
}

function normalizeExplanation(value: string): string {
  let text = value
    .replace(/\r\n/g, '\n')
    // NUL comes back in LLM output often enough to matter, and React throws on it.
    // eslint-disable-next-line no-control-regex
    .replace(/\u0000/g, '')
    .trim();

  const parasiticPrefixes = [
    'Sortie :',
    'Sortie:',
    'Réponse :',
    'Réponse:',
    'Output:',
    'Output :',
    'CAS A —',
    'CAS A -',
    'CAS B —',
    'CAS B -',
  ];

  for (const prefix of parasiticPrefixes) {
    if (text.startsWith(prefix)) {
      text = text.slice(prefix.length).trim();
    }
  }

  const firstRuleIndex = text.search(/\n\s*(Règles|Rules|Exemple|Example|Entrée|Input)\s*:/i);
  if (firstRuleIndex >= 0) {
    text = text.slice(0, firstRuleIndex).trim();
  }

  const boldCount = (text.match(/\*\*/g) || []).length;
  if (boldCount % 2 !== 0) {
    text = text.replace(/\*\*/g, '');
  }

  return text;
}

function inlineFormat(text: string): React.ReactNode {
  const parts: React.ReactNode[] = [];
  const regex = /(\*\*[^*]+\*\*|`[^`]+`)/g;
  let lastIndex = 0;
  let m: RegExpExecArray | null;
  let key = 0;
  while ((m = regex.exec(text)) !== null) {
    if (m.index > lastIndex) parts.push(text.slice(lastIndex, m.index));
    const token = m[0];
    if (token.startsWith('**')) {
      parts.push(<strong key={key++}>{token.slice(2, -2)}</strong>);
    } else {
      parts.push(<code key={key++}>{token.slice(1, -1)}</code>);
    }
    lastIndex = m.index + token.length;
  }
  if (lastIndex < text.length) parts.push(text.slice(lastIndex));
  return parts;
}
