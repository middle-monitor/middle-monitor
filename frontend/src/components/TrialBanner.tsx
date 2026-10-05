import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HiBolt } from 'react-icons/hi2';
import { usePlan } from './PlanGate';
import { useOrgPath } from '../hooks/useOrgPath';
import './TrialBanner.css';

// Shown app-wide while a free org runs its Pro trial. When it expires the org
// falls back to the free caps on its own: existing resources keep running and the
// over-limit banner takes over from here.
export function TrialBanner() {
  const { t } = useTranslation();
  const { isTrial, trialDaysLeft } = usePlan();
  const { orgPath } = useOrgPath();

  if (!isTrial) return null;

  return (
    <div className='trial-banner'>
      <HiBolt className='trial-banner-icon' />
      <span className='trial-banner-text'>{t('trial.message', { count: trialDaysLeft })}</span>
      <Link to={orgPath('/settings#billing')} className='trial-banner-cta'>
        {t('trial.cta')}
      </Link>
    </div>
  );
}
