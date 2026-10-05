import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { HiOutlineExclamationTriangle, HiOutlineXMark } from 'react-icons/hi2';
import { useOrgApi } from '../hooks/useOrgApi';
import { useAuth } from '../contexts/AuthContext';
import './PlanOverLimitBanner.css';

// A dismissal holds for a week: long enough not to nag, short enough that an
// organization still over the limit hears about it again before it bites.
const DISMISS_MS = 7 * 24 * 60 * 60 * 1000;
const POINTS_PER_HOST = 250;

const dismissKey = (slug: string) => `mm_ingest_limit_banner_dismissed:${slug}`;

function dismissedRecently(slug: string): boolean {
  try {
    const at = Number(localStorage.getItem(dismissKey(slug)));
    return at > 0 && Date.now() - at < DISMISS_MS;
  } catch {
    return false;
  }
}

interface Over {
  peak: number;
  limit: number;
  enforced: boolean;
}

// Shown app-wide when the organization's busiest minute of the last hour went
// over its metric ingestion limit, so the admin learns it here and not by
// noticing missing points.
export function IngestLimitBanner() {
  const { t, i18n } = useTranslation();
  const { organization } = useAuth();
  const orgApi = useOrgApi();
  const slug = organization?.slug ?? '';
  const [over, setOver] = useState<Over | null>(null);
  const [dismissed, setDismissed] = useState(() => dismissedRecently(slug));

  useEffect(() => {
    setDismissed(dismissedRecently(slug));
    let cancelled = false;
    orgApi
      .getStats()
      .then((res) => {
        if (cancelled || !res.data) return;
        const u = res.data.plan_usage;
        // Absent until the API that meters ingestion is deployed.
        if (u.points_per_minute_limit === undefined || u.points_per_minute_limit < 0) return;
        if (u.points_per_minute_peak > u.points_per_minute_limit) {
          setOver({ peak: u.points_per_minute_peak, limit: u.points_per_minute_limit, enforced: u.points_limit_enforced });
        }
      })
      .catch(() => {
        /* no banner on error */
      });
    return () => {
      cancelled = true;
    };
  }, [orgApi, slug, organization?.plan]);

  if (!over || dismissed) return null;

  const dismiss = () => {
    try {
      localStorage.setItem(dismissKey(slug), String(Date.now()));
    } catch {
      /* the banner still closes for this visit */
    }
    setDismissed(true);
  };

  return (
    <div className='plan-over-limit-banner' role='status'>
      <HiOutlineExclamationTriangle className='plan-over-limit-icon' />
      <span className='plan-over-limit-text'>
        {t(over.enforced ? 'ingest_limit.message_enforced' : 'ingest_limit.message_observed', {
          peak: over.peak.toLocaleString(i18n.language),
          limit: over.limit.toLocaleString(i18n.language),
          perHost: POINTS_PER_HOST.toLocaleString(i18n.language),
        })}
      </span>
      <a href='/docs#limits' className='plan-over-limit-cta'>
        {t('ingest_limit.cta')}
      </a>
      <button
        type='button'
        className='plan-over-limit-dismiss'
        onClick={dismiss}
        aria-label={t('ingest_limit.dismiss')}
        title={t('ingest_limit.dismiss')}
      >
        <HiOutlineXMark />
      </button>
    </div>
  );
}
