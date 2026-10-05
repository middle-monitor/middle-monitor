import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HiOutlineExclamationTriangle } from 'react-icons/hi2';
import { useOrgApi } from '../hooks/useOrgApi';
import { useAuth } from '../contexts/AuthContext';
import './PlanOverLimitBanner.css';

// Shown app-wide when an org consumes more resources than its current plan allows
// (e.g. after downgrading from Custom to Pro). Existing resources keep running; this
// nudges the admin to reduce usage or move back up a tier.
export function PlanOverLimitBanner() {
  const { t } = useTranslation();
  const { organization } = useAuth();
  const orgApi = useOrgApi();
  const [over, setOver] = useState<string[]>([]);

  useEffect(() => {
    let cancelled = false;
    orgApi.getStats()
      .then((res) => {
        if (cancelled || !res.data) return;
        const u = res.data.plan_usage;
        const items: string[] = [];
        const check = (used: number, limit: number, label: string) => {
          if (limit >= 0 && used > limit) items.push(`${used}/${limit} ${label}`);
        };
        check(u.hosts_used, u.hosts_limit, t('over_limit.hosts'));
        check(u.services_used, u.services_limit, t('over_limit.services'));
        check(u.error_services_used, u.error_services_limit, t('over_limit.sdk'));
        setOver(items);
      })
      .catch(() => { /* no banner on error */ });
    return () => { cancelled = true; };
    // Re-check when the active org or its plan changes.
  }, [orgApi, organization?.slug, organization?.plan, t]);

  if (over.length === 0) return null;

  return (
    <div className='plan-over-limit-banner'>
      <HiOutlineExclamationTriangle className='plan-over-limit-icon' />
      <span className='plan-over-limit-text'>
        {t('over_limit.message', { items: over.join(', ') })}
      </span>
      <Link to='/pricing' className='plan-over-limit-cta'>
        {t('over_limit.cta')}
      </Link>
    </div>
  );
}
