import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { SiGithub } from 'react-icons/si';
import { HiArrowRight, HiBolt } from 'react-icons/hi2';
import { useAuth } from '../contexts/AuthContext';
import { isDemoMode } from '../demo/demoMode';
import { LanguageSwitcher } from './LanguageSwitcher/LanguageSwitcher';
import './SiteHeader.css';

export type SitePage = 'home' | 'docs' | 'pricing' | 'status';

interface SiteHeaderProps {
  active: SitePage;
  // Page-specific controls rendered before the language switcher (Docs: search + theme toggle).
  leadingActions?: React.ReactNode;
  // Page-specific controls rendered after the auth buttons (Docs: mobile nav trigger).
  trailingActions?: React.ReactNode;
}

// Shared marketing-site header: same logo, nav tabs, and auth actions on
// Home, Docs, and Pricing so they no longer drift out of sync with each other.
export function SiteHeader({ active, leadingActions, trailingActions }: SiteHeaderProps) {
  const { t } = useTranslation();
  const { hasStoredSession, organization } = useAuth();
  // Demo sessions report as authenticated: the marketing header must still offer
  // Login / Get Started, not a shortcut back into the sandbox.
  const hasAccount = hasStoredSession && !isDemoMode();
  const dashboardUrl = organization?.slug
    ? `/organizations/${organization.slug}`
    : '/login';

  return (
    <header className='site-header'>
      <div className='site-header-inner'>
        <Link to='/' className='site-logo'>
          <div className='brand-logo-icon brand-logo-icon-md site-logo-icon'>
            <HiBolt />
          </div>
          <span>Middle Monitor</span>
        </Link>

        <nav className='site-nav'>
          <a href='/#platform' className='site-nav-link site-nav-secondary'>
            {t('home.nav.platform')}
          </a>
          <a href='/#features' className='site-nav-link site-nav-secondary'>
            {t('home.nav.features')}
          </a>
          <Link
            to='/docs'
            className={`site-nav-link${active === 'docs' ? ' site-nav-active' : ''}`}>
            {t('public.nav_docs')}
          </Link>
          <Link
            to='/pricing'
            className={`site-nav-link${active === 'pricing' ? ' site-nav-active' : ''}`}>
            {t('public.nav_pricing')}
          </Link>
          <Link to='/contact' className='site-nav-link'>
            {t('public.nav_contact')}
          </Link>
          <a
            href='https://github.com/middle-monitor/middle-monitor'
            target='_blank'
            rel='noopener noreferrer'
            className='site-nav-link site-nav-icon'
            aria-label={t('public.nav_github')}
            title={t('public.nav_github')}>
            <SiGithub />
          </a>
        </nav>

        <div className='site-header-actions'>
          {leadingActions}

          <LanguageSwitcher variant='compact' className='site-lang-switcher' />

          {hasAccount ? (
            <Link
              to={dashboardUrl}
              className='site-btn site-btn-primary site-btn-desktop'>
              {t('public.btn_dashboard')}{' '}
              <HiArrowRight className='inline ml-1 w-4 h-4' />
            </Link>
          ) : (
            <div className='site-btn-desktop site-btn-group'>
              <Link to='/login' className='site-btn site-btn-ghost'>
                {t('public.btn_login')}
              </Link>
              <Link
                to='/get-started'
                className='site-btn site-btn-primary plausible-event-name=Signup+Start'>
                {t('public.btn_get_started')}
              </Link>
            </div>
          )}

          {trailingActions}
        </div>
      </div>
    </header>
  );
}
