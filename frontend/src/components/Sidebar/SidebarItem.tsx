import { Link, useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { NavItem } from '../../config/navigation';
import { useOrgPath } from '../../hooks/useOrgPath';
import { usePlan } from '../PlanGate';

interface SidebarItemProps {
  item: NavItem;
  depth?: number;
  isCollapsed?: boolean;
}

export function SidebarItem({ item, depth = 0, isCollapsed }: SidebarItemProps) {
  const { t } = useTranslation();
  const location = useLocation();
  const { orgPath } = useOrgPath();
  const { billingEnabled } = usePlan();
  const Icon = item.icon;
  const label = t(item.labelKey);
  const fullPath = item.externalPath ?? (item.path ? orgPath(item.path) : undefined);
  const isActive = fullPath === location.pathname;
  const isDisabled = item.disabled;

  const paddingLeft = depth > 0 ? `${1.25 + depth * 0.75}rem` : '0.75rem';

  const getBadgeClass = (badge?: string) => {
    switch (badge) {
      case 'new':
        return '';
      case 'beta':
        return 'sidebar-badge sidebar-badge-beta';
      case 'coming-soon':
        return 'sidebar-badge sidebar-badge-soon';
      case 'pro':
        return 'sidebar-badge sidebar-badge-pro';
      default:
        return '';
    }
  };

  const getBadgeText = (badge?: string) => {
    switch (badge) {
      case 'new':
        return '';
      case 'beta':
        return 'Beta';
      case 'coming-soon':
        return 'Soon';
      case 'pro':
        return 'Pro';
      default:
        return '';
    }
  };

  if (isDisabled) {
    return (
      <div
        className='sidebar-item sidebar-item-disabled'
        style={{ paddingLeft }}
        data-tooltip={isCollapsed ? label : undefined}
      >
        <Icon className='sidebar-item-icon' />
        {!isCollapsed && <span className='sidebar-item-label'>{label}</span>}
        {!isCollapsed && item.badge && (item.badge !== 'pro' || billingEnabled) && (
          <span className={getBadgeClass(item.badge)}>
            {getBadgeText(item.badge)}
          </span>
        )}
      </div>
    );
  }

  return (
    <Link
      to={fullPath || '/'}
      className={`sidebar-item ${isActive ? 'sidebar-item-active' : ''}`}
      style={{ paddingLeft }}
      data-tooltip={isCollapsed ? label : undefined}
    >
      <Icon className='sidebar-item-icon' />
      {!isCollapsed && <span className='sidebar-item-label'>{label}</span>}
      {!isCollapsed && item.badge && (item.badge !== 'pro' || billingEnabled) && (
        <span className={getBadgeClass(item.badge)}>
          {getBadgeText(item.badge)}
        </span>
      )}
    </Link>
  );
}
