import { useAuth } from '../contexts/AuthContext';
import { useOrgPath } from '../hooks/useOrgPath';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HiBolt } from 'react-icons/hi2';

interface PlanGateProps {
  feature: string;
  children: React.ReactNode;
}

export function usePlan() {
  const { organization } = useAuth();
  // The API sends the effective plan: a free org inside its trial arrives as 'pro',
  // with the deadline attached.
  const plan = organization?.plan ?? 'free';
  // Absent from older payloads and the demo, which both sell plans.
  const billingEnabled = organization?.billing_enabled !== false;
  const trialEndsAt = organization?.trial_ends_at ? new Date(organization.trial_ends_at) : null;
  const trialMsLeft = trialEndsAt ? trialEndsAt.getTime() - Date.now() : 0;
  const isTrial = trialMsLeft > 0;
  return {
    // Paid plans (pro and custom) unlock the same gated features; so does a trial.
    isPro: plan === 'pro' || plan === 'custom',
    isTrial,
    billingEnabled,
    trialDaysLeft: isTrial ? Math.ceil(trialMsLeft / 86400000) : 0,
    // Billing flows must not treat a trial as a Stripe subscription to manage.
    isPaidSubscriber: (plan === 'pro' || plan === 'custom') && !isTrial,
    plan,
  };
}

export function PlanGate({ feature, children }: PlanGateProps) {
  const { isPro } = usePlan();
  if (isPro) return <>{children}</>;
  return <ProUpgradeBanner feature={feature} />;
}

interface ProUpgradeBannerProps {
  feature: string;
  inline?: boolean;
}

export function ProUpgradeBanner({ feature, inline }: ProUpgradeBannerProps) {
  const { orgPath } = useOrgPath();
  const { t } = useTranslation();

  if (inline) {
    return (
      <span
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: '0.3rem',
          background: 'linear-gradient(135deg, rgba(139,92,246,0.15), rgba(59,130,246,0.12))',
          border: '1px solid rgba(139,92,246,0.3)',
          borderRadius: 999,
          padding: '0.15rem 0.6rem',
          fontSize: '0.7rem',
          fontWeight: 700,
          color: '#7c3aed',
          cursor: 'default',
        }}
      >
        <HiBolt style={{ fontSize: '0.75rem' }} /> Pro
      </span>
    );
  }

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: '1rem',
        padding: '3rem 1.5rem',
        background: 'linear-gradient(135deg, rgba(139,92,246,0.06), rgba(59,130,246,0.04))',
        border: '1px solid rgba(139,92,246,0.2)',
        borderRadius: '12px',
        textAlign: 'center',
      }}
    >
      <div style={{ fontSize: '2.5rem' }}>
        <HiBolt style={{ color: '#7c3aed' }} />
      </div>
      <div>
        <h3 style={{ margin: '0 0 0.4rem', fontWeight: 700 }}>
          {t('plan_gate.pro_feature', { feature })}
        </h3>
        <p style={{ margin: 0, color: 'var(--text-secondary)', fontSize: '0.9rem', maxWidth: '400px' }}>
          {t('plan_gate.upgrade_desc', { feature: feature.toLowerCase() })}
        </p>
      </div>
      <Link
        to={orgPath('/settings#billing')}
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: '0.4rem',
          background: 'linear-gradient(135deg, #7c3aed, #3b82f6)',
          color: '#fff',
          fontWeight: 700,
          borderRadius: '8px',
          padding: '0.6rem 1.4rem',
          textDecoration: 'none',
          fontSize: '0.9rem',
        }}
      >
        <HiBolt /> {t('plan_gate.upgrade_cta')}
      </Link>
    </div>
  );
}

// PlanLimitNotice is shown inline in creation forms when the backend rejects a
// host/service with a plan-quota error (response code "plan_limit"). It tells the
// user the cap was hit and links to the billing page to raise it.
export function PlanLimitNotice({ message }: { message?: string }) {
  const { t } = useTranslation();
  const { orgPath } = useOrgPath();

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '0.75rem',
        padding: '1rem',
        marginBottom: '1rem',
        background: 'linear-gradient(135deg, rgba(139,92,246,0.08), rgba(59,130,246,0.05))',
        border: '1px solid rgba(139,92,246,0.3)',
        borderRadius: '10px',
      }}
    >
      <div style={{ display: 'flex', gap: '0.6rem', alignItems: 'flex-start' }}>
        <HiBolt style={{ color: '#7c3aed', flexShrink: 0, marginTop: 2 }} />
        <div>
          <p style={{ margin: 0, fontWeight: 700 }}>{t('plan_limit.title')}</p>
          {message && (
            <p style={{ margin: '0.25rem 0 0', fontSize: '0.85rem', color: 'var(--text-secondary)' }}>
              {message}
            </p>
          )}
        </div>
      </div>
      <Link
        to={orgPath('/settings#billing')}
        style={{
          display: 'inline-flex',
          alignSelf: 'flex-start',
          alignItems: 'center',
          gap: '0.4rem',
          background: 'linear-gradient(135deg, #7c3aed, #3b82f6)',
          color: '#fff',
          fontWeight: 700,
          borderRadius: '8px',
          padding: '0.5rem 1.1rem',
          textDecoration: 'none',
          fontSize: '0.85rem',
        }}
      >
        <HiBolt /> {t('plan_limit.cta')}
      </Link>
    </div>
  );
}
