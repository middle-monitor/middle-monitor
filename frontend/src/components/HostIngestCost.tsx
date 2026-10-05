import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { HiOutlineClipboard, HiOutlineCheck, HiOutlineLightBulb } from 'react-icons/hi2';
import { useOrgApi } from '../hooks/useOrgApi';
import type { HostIngestCost as HostIngestCostData, TargetIngestCost } from '../api';
import { suggestionsFor, type IngestSuggestion } from './ingestSuggestions';
import './HostIngestCost.css';

/** What each scrape target of a host costs against the metric budget, and what to change. */
export function HostIngestCost({ hostId }: { hostId: number }) {
  const { t, i18n } = useTranslation();
  const orgApi = useOrgApi();
  const [cost, setCost] = useState<HostIngestCostData | null>(null);

  useEffect(() => {
    let cancelled = false;
    orgApi.hosts
      .ingestCost(hostId)
      .then((res) => {
        if (!cancelled) setCost(res.data);
      })
      .catch(() => {
        if (!cancelled) setCost(null);
      });
    return () => {
      cancelled = true;
    };
  }, [orgApi, hostId]);

  if (!cost || cost.targets.length === 0) return null;

  const fmt = (n: number) => n.toLocaleString(i18n.language);
  const limit = cost.org_points_per_minute_limit;
  const share = (points: number) => (limit > 0 ? `${Math.round((points / limit) * 100)}%` : '—');

  return (
    <div className='card host-ingest'>
      <div className='host-ingest-header'>
        <h3>{t('host_detail.ingest.title')}</h3>
        <p>
          {limit > 0
            ? t('host_detail.ingest.subtitle', { minutes: cost.window_minutes, points: fmt(cost.points_per_minute), limit: fmt(limit) })
            : t('host_detail.ingest.subtitle_unlimited', { minutes: cost.window_minutes, points: fmt(cost.points_per_minute) })}
        </p>
      </div>
      <table className='host-ingest-table'>
        <thead>
          <tr>
            <th>{t('host_detail.ingest.target')}</th>
            <th>{t('host_detail.ingest.series')}</th>
            <th>{t('host_detail.ingest.interval')}</th>
            <th>{t('host_detail.ingest.points')}</th>
            <th>{t('host_detail.ingest.share')}</th>
          </tr>
        </thead>
        <tbody>
          {cost.targets.map((target) => (
            <TargetRow key={target.name || '_system'} target={target} fmt={fmt} share={share(target.points_per_minute)} />
          ))}
        </tbody>
      </table>
      <p className='host-ingest-footnote'>
        {t('host_detail.ingest.footnote')} <a href='/docs#limits'>{t('host_detail.ingest.docs')}</a>
      </p>
    </div>
  );
}

function TargetRow({ target, fmt, share }: { target: TargetIngestCost; fmt: (n: number) => string; share: string }) {
  const { t } = useTranslation();
  const suggestions = suggestionsFor(target);
  return (
    <>
      <tr>
        <td>{target.name || t('host_detail.ingest.system')}</td>
        <td>{fmt(target.series)}</td>
        <td>{target.scrape_interval_seconds > 0 ? `${target.scrape_interval_seconds}s` : '—'}</td>
        <td>{fmt(target.points_per_minute)}</td>
        <td>{share}</td>
      </tr>
      {suggestions.map((s) => (
        <tr key={s.kind} className='host-ingest-suggestion-row'>
          <td colSpan={5}>
            <Suggestion suggestion={s} target={target} fmt={fmt} />
          </td>
        </tr>
      ))}
    </>
  );
}

function Suggestion({
  suggestion,
  target,
  fmt,
}: {
  suggestion: IngestSuggestion;
  target: TargetIngestCost;
  fmt: (n: number) => string;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const copy = () => {
    navigator.clipboard
      ?.writeText(suggestion.yaml)
      .then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 2000);
      })
      .catch(() => {
        /* the snippet stays selectable */
      });
  };

  return (
    <div className='host-ingest-suggestion'>
      <div className='host-ingest-suggestion-text'>
        <HiOutlineLightBulb aria-hidden='true' />
        <span>
          {t(`host_detail.ingest.suggest_${suggestion.kind}`, {
            saves: fmt(suggestion.saves),
            interval: target.scrape_interval_seconds,
            target: target.name,
          })}
        </span>
      </div>
      <div className='host-ingest-snippet'>
        <pre>{suggestion.yaml}</pre>
        <button type='button' onClick={copy} aria-label={t('host_detail.ingest.copy')} title={t('host_detail.ingest.copy')}>
          {copied ? <HiOutlineCheck /> : <HiOutlineClipboard />}
        </button>
      </div>
    </div>
  );
}

export default HostIngestCost;
