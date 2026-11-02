import { useTranslation } from 'react-i18next';
import { HiOutlineEye } from 'react-icons/hi2';
import { SiGithub } from 'react-icons/si';
import { isDemoMode, exitDemoMode } from '../demo/demoMode';
import './DemoBanner.css';

/**
 * Shown at the top of the dashboard while demo mode is active. Reminds the
 * visitor that everything is simulated and offers the only supported exit:
 * clearing the flag and reloading from the landing page.
 */
export function DemoBanner() {
  const { t } = useTranslation();

  if (!isDemoMode()) return null;

  const handleExit = () => {
    exitDemoMode();
    window.location.href = '/';
  };

  return (
    <div className="demo-banner">
      <HiOutlineEye className="demo-banner-icon" />
      <span className="demo-banner-text">{t('demo.banner')}</span>
      <a
        href="https://github.com/middle-monitor/middle-monitor"
        target="_blank"
        rel="noopener noreferrer"
        className="btn btn-ghost demo-banner-github plausible-event-name=GitHub+Star">
        <SiGithub />
        {t('demo.github')}
      </a>
      <button type="button" className="btn btn-primary demo-banner-exit" onClick={handleExit}>
        {t('demo.exit')}
      </button>
    </div>
  );
}
