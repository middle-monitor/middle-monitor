import { useState, useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HiChevronDown } from 'react-icons/hi2';
import { NavSection } from '../../config/navigation';
import { useOrgPath } from '../../hooks/useOrgPath';
import { useAuth } from '../../contexts/AuthContext';
import { SidebarItem } from './SidebarItem';

interface SidebarSectionProps {
  section: NavSection;
  isCollapsed?: boolean;
}

const STORAGE_KEY_PREFIX = 'sidebar-section-';

export function SidebarSection({ section, isCollapsed }: SidebarSectionProps) {
  const { t } = useTranslation();
  const location = useLocation();
  const { orgPath } = useOrgPath();
  const { isAdmin } = useAuth();
  const storageKey = `${STORAGE_KEY_PREFIX}${section.id}`;

  // adminOnly items (e.g. org settings) are hidden from non-admins.
  const items = section.items.filter((item) => !item.adminOnly || isAdmin);

  // Check if any child is active (compare with org-prefixed paths)
  const hasActiveChild = items.some(
    (item) => item.path && orgPath(item.path) === location.pathname
  );

  // Initialize expanded state from localStorage or default
  const [isExpanded, setIsExpanded] = useState(() => {
    const stored = localStorage.getItem(storageKey);
    if (stored !== null) {
      return stored === 'true';
    }
    return section.defaultExpanded ?? true;
  });

  // Auto-expand if a child becomes active
  useEffect(() => {
    if (hasActiveChild && !isExpanded) {
      setIsExpanded(true);
      localStorage.setItem(storageKey, 'true');
    }
  }, [hasActiveChild, isExpanded, storageKey]);

  const toggleExpanded = () => {
    if (isCollapsed) return; // Disable toggle when collapsed
    const newValue = !isExpanded;
    setIsExpanded(newValue);
    localStorage.setItem(storageKey, String(newValue));
  };

  // Nothing visible for this user (e.g. section with only admin-only items).
  if (items.length === 0) return null;

  return (
    <div className={`sidebar-section ${section.separatorBefore ? 'sidebar-section-separated' : ''}`}>
      {!isCollapsed && (
        <button
          className={`sidebar-section-header ${hasActiveChild ? 'sidebar-section-header-active' : ''}`}
          onClick={toggleExpanded}
          aria-expanded={isExpanded}
        >
          <span className="sidebar-section-label">{t(section.labelKey)}</span>
          <HiChevronDown
            className={`sidebar-section-chevron ${isExpanded ? 'sidebar-section-chevron-expanded' : ''}`}
          />
        </button>
      )}

      <div
        className={`sidebar-section-content ${isExpanded || isCollapsed ? 'sidebar-section-content-expanded' : ''}`}
      >
        <div className="sidebar-section-items">
          {items.map((item) => (
            <SidebarItem key={item.id} item={item} isCollapsed={isCollapsed} />
          ))}
        </div>
      </div>
    </div>
  );
}
