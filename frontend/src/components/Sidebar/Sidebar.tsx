import { useState, useEffect } from 'react';
import { Link, useNavigate, useLocation } from 'react-router-dom';
import {
  HiBolt,
  HiSun,
  HiMoon,
  HiArrowRightOnRectangle,
  HiUser,
  HiBars3BottomLeft,
  HiBars3,
  HiLanguage,
  HiChevronUpDown,
  HiCheck,
  HiOutlineBuildingOffice2,
} from 'react-icons/hi2';
import { useTranslation } from 'react-i18next';
import { useTheme } from '../../contexts/ThemeContext';
import { useAuth } from '../../contexts/AuthContext';
import { UserOrganization } from '../../api';
import { useOrgPath } from '../../hooks/useOrgPath';
import { navigationConfig } from '../../config/navigation';
import { SidebarSection } from './SidebarSection';
import './Sidebar.css';

export function Sidebar() {
  const { t, i18n } = useTranslation();
  const { toggleTheme, isDark } = useTheme();
  const { user, organization, organizations, switchOrg, logout, isPlatformAdmin } = useAuth();
  const { orgBase } = useOrgPath();
  const navigate = useNavigate();
  const location = useLocation();
  const [showOrgMenu, setShowOrgMenu] = useState(false);
  const [isCollapsed, setIsCollapsed] = useState(() => {
    return localStorage.getItem('sidebar_collapsed') === 'true';
  });
  // Drawer state, mobile only: the sidebar is off-canvas under 768px and has no
  // other way in. Not persisted — it should always open closed.
  const [isOpen, setIsOpen] = useState(false);

  // The collapsed rail is persisted across sessions, but it only makes sense
  // next to content. The trigger that sets isOpen is mobile-only, so an open
  // drawer always renders expanded while desktop keeps the stored state.
  const showCollapsed = isCollapsed && !isOpen;

  const handleSwitchOrg = async (org: UserOrganization) => {
    setShowOrgMenu(false);
    if (org.id === organization?.id) return;
    try {
      await switchOrg(org.id);
      navigate(`/organizations/${org.slug}`);
    } catch {
      /* switching failed (e.g. membership revoked); stay where we are */
    }
  };

  const toggleLanguage = () => {
    const nextLang = i18n.language.startsWith('fr') ? 'en' : 'fr';
    i18n.changeLanguage(nextLang);
    localStorage.setItem('appLang', nextLang);
  };

  useEffect(() => {
    localStorage.setItem('sidebar_collapsed', isCollapsed.toString());
    if (isCollapsed) {
      document.body.classList.add('sidebar-collapsed');
    } else {
      document.body.classList.remove('sidebar-collapsed');
    }
  }, [isCollapsed]);

  // Tapping a nav entry navigates without unmounting the sidebar, so the drawer
  // would stay over the page it just opened.
  useEffect(() => {
    setIsOpen(false);
  }, [location.pathname]);

  return (
    <>
      {/* Drawer trigger and backdrop: CSS keeps both hidden above 768px. */}
      <button
        className="sidebar-mobile-toggle"
        onClick={() => setIsOpen(true)}
        aria-label={t('sidebar.open')}
        aria-expanded={isOpen}
      >
        <HiBars3 size={22} />
      </button>
      <div
        className={`sidebar-backdrop ${isOpen ? 'visible' : ''}`}
        onClick={() => setIsOpen(false)}
        aria-hidden="true"
      />
      <aside className={`sidebar ${showCollapsed ? 'collapsed' : ''} ${isOpen ? 'open' : ''}`}>
      {/* Brand Header */}
      <div className="sidebar-header">
        <Link to={orgBase} className="sidebar-brand">
          <div className="brand-logo-icon brand-logo-icon-lg sidebar-brand-icon">
            <HiBolt />
          </div>
          {!showCollapsed && <span className="sidebar-brand-text">Middle Monitor</span>}
        </Link>
        <button
          className="sidebar-collapse-btn"
          onClick={() => setIsCollapsed(!isCollapsed)}
          data-tooltip={isCollapsed ? t('sidebar.open') : t('sidebar.collapse')}
        >
          <HiBars3BottomLeft size={20} />
        </button>
      </div>

      {/* Navigation */}
      <nav className="sidebar-nav">
        {navigationConfig.map((section) => (
          <SidebarSection key={section.id} section={section} isCollapsed={showCollapsed} />
        ))}
      </nav>

      {/* Footer */}
      <div className="sidebar-footer">
        {/* Org switcher menu (only when the user belongs to several orgs) */}
        {!showCollapsed && showOrgMenu && organizations.length > 1 && (
          <div className="sidebar-org-menu">
            {organizations.map((org) => (
              <button
                key={org.id}
                className="sidebar-org-option"
                onClick={() => handleSwitchOrg(org)}
              >
                <span className="sidebar-org-option-name">{org.name}</span>
                {org.id === organization?.id && <HiCheck className="sidebar-org-option-check" />}
              </button>
            ))}
          </div>
        )}

        {/* User info */}
        <div className="sidebar-user" data-tooltip={showCollapsed ? t('sidebar.profile') : undefined}>
          <div className="sidebar-user-avatar">
            <HiUser />
          </div>
          {!showCollapsed && (
            organizations.length > 1 ? (
              <button
                className="sidebar-user-details sidebar-org-switcher"
                onClick={() => setShowOrgMenu((v) => !v)}
                title={t('sidebar.switch_org')}
              >
                <span className="sidebar-user-name">{user?.name}</span>
                <span className="sidebar-user-org">
                  {organization?.name}
                  <HiChevronUpDown className="sidebar-org-switcher-icon" />
                </span>
              </button>
            ) : (
              <div className="sidebar-user-details">
                <span className="sidebar-user-name">{user?.name}</span>
                <span className="sidebar-user-org">{organization?.name}</span>
              </div>
            )
          )}
        </div>

        {/* Cross-org admin page: instance owner only. */}
        {isPlatformAdmin && (
          <Link className="sidebar-action" to="/platform-admin" data-tooltip={showCollapsed ? t('platform_admin.title') : undefined}>
            <HiOutlineBuildingOffice2 className="sidebar-action-icon" />
            {!showCollapsed && <span>{t('platform_admin.title')}</span>}
          </Link>
        )}

        {/* Language toggle */}
        <button className="sidebar-action" onClick={toggleLanguage} data-tooltip={showCollapsed ? t('sidebar.language') : undefined}>
          <HiLanguage className="sidebar-action-icon" />
          {!showCollapsed && <span>{i18n.language.startsWith('fr') ? 'Français' : 'English'}</span>}
        </button>

        {/* Theme toggle */}
        <button className="sidebar-action" onClick={toggleTheme} data-tooltip={showCollapsed ? t('sidebar.theme') : undefined}>
          {isDark ? (
            <>
              <HiSun className="sidebar-action-icon" />
              {!showCollapsed && <span>{t('sidebar.theme_light')}</span>}
            </>
          ) : (
            <>
              <HiMoon className="sidebar-action-icon" />
              {!showCollapsed && <span>{t('sidebar.theme_dark')}</span>}
            </>
          )}
        </button>

        {/* Logout */}
        <button className="sidebar-action sidebar-action-logout" onClick={logout} data-tooltip={showCollapsed ? t('sidebar.logout') : undefined}>
          <HiArrowRightOnRectangle className="sidebar-action-icon" />
          {!showCollapsed && <span>{t('sidebar.logout')}</span>}
        </button>
      </div>
      </aside>
    </>
  );
}
