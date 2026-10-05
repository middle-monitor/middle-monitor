import { useState, useRef, useEffect } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { LanguageSwitcher } from '../components/LanguageSwitcher/LanguageSwitcher';
import { SiteHeader } from '../components/SiteHeader';
import {
  CustomPlanCalculator,
  type CustomPlanValues,
  PRO_BASE_PRICE,
  PRO_HOSTS,
  PRO_SERVICES,
  PRO_SDK,
} from '../components/CustomPlanCalculator';
import { HiArrowRight, HiBolt, HiCheck, HiOutlineCube, HiOutlineServerStack, HiOutlineChartBar, HiOutlineExclamationTriangle, HiXMark, HiOutlineCreditCard } from 'react-icons/hi2';
import { useAuth } from '../contexts/AuthContext';
import { usePlan } from '../components/PlanGate';
import { useOrgApi } from '../hooks/useOrgApi';
import { isDemoMode } from '../demo/demoMode';
import { useDocumentMeta } from '../seo/useDocumentMeta';
import './PricingView.css';

export default function PricingView() {
  const { t } = useTranslation();
  useDocumentMeta('pricing');
  const navigate = useNavigate();
  const { isAuthenticated, isAdmin, organization, refreshUser } = useAuth();
  const { isTrial } = usePlan();
  const orgApi = useOrgApi();
  const [notAdminError, setNotAdminError] = useState(false);
  const [isUpdating, setIsUpdating] = useState(false);
  const [showUpdateConfirm, setShowUpdateConfirm] = useState(false);
  const [updateError, setUpdateError] = useState(false);
  const [pendingUpdate, setPendingUpdate] = useState<CustomPlanValues | null>(null);
  const notAdminRef = useRef<HTMLDivElement>(null);

  const [calcValues, setCalcValues] = useState<CustomPlanValues>({
    hosts: 15,
    services: 80,
    sdk_services: 10,
    retention_days: 30,
    total: PRO_BASE_PRICE,
  });

  const dashboardUrl = organization?.slug ? `/organizations/${organization.slug}` : '/login';
  // A demo session reports as authenticated with a fake Pro org: pricing must keep
  // treating the visitor as a prospect, not as a subscriber.
  const hasAccount = isAuthenticated && !isDemoMode();
  // A trial reads as 'pro' but has nothing to manage: keep it on the buying path.
  const currentPlan = hasAccount ? (isTrial ? 'free' : (organization?.plan ?? 'free')) : null;
  const settingsBillingUrl = organization?.slug ? `/organizations/${organization.slug}/settings#billing` : '/login';
  const isPaidSubscriber = currentPlan === 'pro' || currentPlan === 'custom';

  useEffect(() => {
    if (notAdminError) {
      notAdminRef.current?.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
  }, [notAdminError]);

  const FREE_FEATURES = [
    { text: t('pricing.free.f_trial'), icon: HiBolt },
    { text: t('pricing.free.f_hosts'), icon: HiOutlineServerStack },
    { text: t('pricing.free.f_services'), icon: HiOutlineCube },
    { text: t('pricing.free.f_sdk'), icon: HiOutlineExclamationTriangle },
    { text: t('pricing.free.f_metrics'), icon: HiOutlineChartBar },
    { text: t('pricing.free.f_points'), icon: HiOutlineChartBar },
    { text: t('pricing.free.f_retention'), icon: HiOutlineChartBar },
    { text: t('pricing.free.f_support'), icon: HiBolt },
  ];

  const PRO_FEATURES = [
    t('pricing.pro.f_hosts'),
    t('pricing.pro.f_services'),
    t('pricing.pro.f_sdk'),
    t('pricing.pro.f_metrics'),
    t('pricing.pro.f_points'),
    t('pricing.pro.f_profiling'),
    t('pricing.pro.f_retention'),
    t('pricing.pro.f_alerting'),
    t('pricing.pro.f_rca'),
  ];

  const openUpdateConfirm = (target: CustomPlanValues) => {
    setPendingUpdate(target);
    setUpdateError(false);
    setShowUpdateConfirm(true);
  };

  const confirmUpdate = async () => {
    if (!pendingUpdate) return;
    setIsUpdating(true);
    setUpdateError(false);
    try {
      await orgApi.payments.updateSubscription({
        hosts: pendingUpdate.hosts,
        services: pendingUpdate.services,
        sdk_services: pendingUpdate.sdk_services,
        retention_days: pendingUpdate.retention_days,
      });
      await refreshUser();
      setShowUpdateConfirm(false);
      navigate(settingsBillingUrl);
    } catch {
      setUpdateError(true);
    } finally {
      setIsUpdating(false);
    }
  };

  const handleCustomClick = async (e: React.MouseEvent) => {
    e.preventDefault();
    if (hasAccount && !isAdmin) {
      setNotAdminError(true);
      return;
    }
    // Existing subscriber: confirm in a card, then modify the running subscription
    // (Stripe prorates and charges the saved card) instead of opening a second checkout.
    if (isPaidSubscriber) {
      openUpdateConfirm(calcValues);
      return;
    }
    if (hasAccount) {
      try {
        const res = await orgApi.payments.checkoutCustom({
          hosts: calcValues.hosts,
          services: calcValues.services,
          sdk_services: calcValues.sdk_services,
          retention_days: calcValues.retention_days,
        });
        if (res.data?.url) {
          window.location.href = res.data.url;
          return;
        }
      } catch {
        // fall through to navigate
      }
    }
    navigate('/get-started?plan=custom');
  };

  const handleProClick = async (e: React.MouseEvent) => {
    e.preventDefault();
    if (hasAccount && !isAdmin) {
      setNotAdminError(true);
      return;
    }
    // Already on Pro: nothing to buy, send them to manage.
    if (currentPlan === 'pro') {
      navigate(settingsBillingUrl);
      return;
    }
    // On Custom: downgrade to base Pro by removing every add-on (Stripe credits the
    // unused difference). Confirm the new €49 total in the card first.
    if (currentPlan === 'custom') {
      openUpdateConfirm({
        hosts: PRO_HOSTS,
        services: PRO_SERVICES,
        sdk_services: PRO_SDK,
        retention_days: 30,
        total: PRO_BASE_PRICE,
      });
      return;
    }
    if (hasAccount) {
      try {
        const res = await orgApi.payments.checkout({ plan: 'pro' });
        if (res.data?.url) {
          window.location.href = res.data.url;
          return;
        }
      } catch {
        // fall through to navigate
      }
    }
    navigate('/get-started?plan=pro');
  };

  return (
    <div className="pricing-view">
      <SiteHeader active="pricing" />

      <main className="pricing-main">
        <div className="pricing-container">
          <div className="pricing-hero">
            <h1 className="pricing-title">{t('pricing.title')}</h1>
            <p className="pricing-subtitle">{t('pricing.subtitle')}</p>
          </div>

          {notAdminError && (
            <div ref={notAdminRef} className="pricing-not-admin-banner">
              {t('pricing.not_admin_error', { org: organization?.name ?? '' })}
            </div>
          )}

          <div className="pricing-grid">
            {/* Free */}
            <div className="pricing-card">
              <div className="pricing-card-header">
                <h2 className="pricing-card-name">{t('pricing.free.name')}</h2>
                <p className="pricing-card-desc">{t('pricing.free.description')}</p>
                <div className="pricing-card-price">
                  <span className="pricing-card-currency">€</span>
                  <span className="pricing-card-amount">0</span>
                  <span className="pricing-card-period">{t('pricing.per_month')}</span>
                </div>
              </div>
              <ul className="pricing-card-features">
                {FREE_FEATURES.map((f, i) => (
                  <li key={i} className="pricing-card-feature">
                    <HiCheck className="pricing-card-feature-icon" />
                    <span>{f.text}</span>
                  </li>
                ))}
              </ul>
              <Link to="/get-started" className="pricing-card-cta pricing-card-cta-secondary">
                {t('pricing.free.cta')}
              </Link>
            </div>

            {/* Pro (featured) */}
            <div className="pricing-card pricing-card-featured">
              <div className="pricing-card-badge">{t('pricing.popular')}</div>
              <div className="pricing-card-header">
                <h2 className="pricing-card-name pricing-card-name-pro">{t('pricing.pro.name')}</h2>
                <p className="pricing-card-desc">{t('pricing.pro.description')}</p>
                <div className="pricing-card-price">
                  <span className="pricing-card-currency pricing-card-currency-pro">€</span>
                  <span className="pricing-card-amount pricing-card-amount-pro">49</span>
                  <span className="pricing-card-period">{t('pricing.per_month')}</span>
                </div>
              </div>
              <ul className="pricing-card-features">
                {PRO_FEATURES.map((text, i) => (
                  <li key={i} className="pricing-card-feature pricing-card-feature-pro">
                    <HiCheck className="pricing-card-feature-icon pricing-card-feature-icon-pro" />
                    <span>{text}</span>
                  </li>
                ))}
              </ul>
              <button onClick={handleProClick} className="pricing-card-cta pricing-card-cta-primary">
                {currentPlan === 'pro'
                  ? t('pricing.manage_current')
                  : currentPlan === 'custom'
                    ? t('pricing.switch_to_pro')
                    : <>{t('pricing.pro.cta')} <HiArrowRight style={{ marginLeft: '0.5rem' }} /></>}
              </button>
            </div>

            {/* Custom (interactive) */}
            <div className="pricing-card pricing-card-custom">
              <div className="pricing-card-header">
                <h2 className="pricing-card-name">{t('pricing.enterprise.name')}</h2>
                <p className="pricing-card-desc">{t('pricing.enterprise.description')}</p>
                <div className="pricing-card-price">
                  <span className="pricing-card-currency pricing-card-currency-custom">€</span>
                  <span className="pricing-card-amount pricing-card-amount-custom">{calcValues.total}</span>
                  <span className="pricing-card-period">{t('pricing.per_month')}</span>
                </div>
              </div>

              <div className="pricing-calculator">
                <CustomPlanCalculator onValuesChange={setCalcValues} />
              </div>

              <button onClick={handleCustomClick} disabled={isUpdating} className="pricing-card-cta pricing-card-cta-custom">
                {isUpdating
                  ? t('pricing.updating')
                  : isPaidSubscriber
                    ? <>{t('pricing.update_cta')} <HiArrowRight style={{ marginLeft: '0.5rem' }} /></>
                    : <>{t('pricing.calculator.cta')} <HiArrowRight style={{ marginLeft: '0.5rem' }} /></>}
              </button>
            </div>
          </div>
        </div>
      </main>

      <footer className="pricing-footer">
        <div className="pricing-container">
          <div className="pricing-footer-actions">
            <LanguageSwitcher variant="compact" className="pricing-footer-lang" />
          </div>
          <div className="pricing-footer-brand">
            <div className="brand-logo-icon brand-logo-icon-sm"><HiBolt /></div>
            <span>Middle Monitor</span>
          </div>
          <div className="pricing-footer-links">
            <Link to="/">{t('public.nav_home')}</Link>
            <Link to="/docs">{t('public.nav_docs')}</Link>
            <Link to="/pricing">{t('public.nav_pricing')}</Link>
            {hasAccount ? (
              <Link to={dashboardUrl}>{t('public.btn_dashboard')}</Link>
            ) : (
              <Link to="/login">{t('public.btn_login')}</Link>
            )}
            <Link to="/alternatives">{t('public.nav_alternatives')}</Link>
            <Link to="/legal">{t('public.nav_legal')}</Link>
            <Link to="/privacy">{t('public.nav_privacy')}</Link>
            <Link to="/terms">{t('public.nav_terms')}</Link>
          </div>
          <p className="pricing-footer-copy">
            © {new Date().getFullYear()} Middle Monitor. {t('pricing.copyright')}
          </p>
        </div>
      </footer>

      {showUpdateConfirm && (
        <div className="pricing-modal-overlay" onClick={() => !isUpdating && setShowUpdateConfirm(false)}>
          <div className="pricing-modal" onClick={(e) => e.stopPropagation()}>
            <button
              className="pricing-modal-close"
              onClick={() => setShowUpdateConfirm(false)}
              disabled={isUpdating}
              aria-label={t('common.close')}
            >
              <HiXMark />
            </button>
            <div className="pricing-modal-icon"><HiOutlineCreditCard /></div>
            <h3 className="pricing-modal-title">{t('pricing.update_modal_title')}</h3>
            <div className="pricing-modal-amount">
              €{pendingUpdate?.total ?? calcValues.total}<span>{t('pricing.per_month')}</span>
            </div>
            <p className="pricing-modal-note">{t('pricing.update_modal_note')}</p>
            {updateError && (
              <p className="pricing-modal-error">{t('pricing.update_failed')}</p>
            )}
            <div className="pricing-modal-actions">
              <button
                className="pricing-modal-btn pricing-modal-btn-ghost"
                onClick={() => setShowUpdateConfirm(false)}
                disabled={isUpdating}
              >
                {t('common.cancel')}
              </button>
              <button
                className="pricing-modal-btn pricing-modal-btn-primary"
                onClick={confirmUpdate}
                disabled={isUpdating}
              >
                {isUpdating ? t('pricing.updating') : t('pricing.update_modal_confirm')}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
