import { ReactNode } from 'react';

export interface Tab {
  id: string;
  label: string;
  count?: number;
  icon?: ReactNode;
}

interface TabsProps {
  tabs: Tab[];
  activeTab: string;
  onChange: (tabId: string) => void;
  variant?: 'default' | 'pills' | 'underline';
  size?: 'sm' | 'md' | 'lg';
}

export function Tabs({
  tabs,
  activeTab,
  onChange,
  variant = 'default',
  size = 'md',
}: TabsProps) {
  const sizeStyles = {
    sm: {
      padding: '0.375rem 0.75rem',
      fontSize: '0.75rem',
      gap: '0.375rem',
    },
    md: {
      padding: '0.5rem 1rem',
      fontSize: '0.8125rem',
      gap: '0.5rem',
    },
    lg: {
      padding: '0.625rem 1.25rem',
      fontSize: '0.875rem',
      gap: '0.625rem',
    },
  };

  const currentSize = sizeStyles[size];

  const baseTabStyle: React.CSSProperties = {
    display: 'inline-flex',
    alignItems: 'center',
    gap: currentSize.gap,
    padding: currentSize.padding,
    fontSize: currentSize.fontSize,
    fontWeight: 500,
    cursor: 'pointer',
    border: 'none',
    background: 'transparent',
    color: 'var(--text-secondary)',
    transition: 'all 0.15s ease',
    position: 'relative',
    whiteSpace: 'nowrap',
  };

  const getVariantStyles = (isActive: boolean): React.CSSProperties => {
    switch (variant) {
      case 'pills':
        return {
          borderRadius: '6px',
          background: isActive ? 'var(--brand-primary-light)' : 'transparent',
          color: isActive ? 'var(--brand-primary)' : 'var(--text-secondary)',
          border: isActive
            ? '1px solid var(--brand-primary-border)'
            : '1px solid transparent',
        };
      case 'underline':
        return {
          borderRadius: 0,
          borderBottom: isActive
            ? '2px solid var(--brand-primary)'
            : '2px solid transparent',
          color: isActive ? 'var(--brand-primary)' : 'var(--text-secondary)',
          paddingBottom: `calc(${currentSize.padding.split(' ')[0]} - 2px)`,
        };
      default:
        return {
          borderRadius: '6px',
          background: isActive ? 'var(--bg-tertiary)' : 'transparent',
          color: isActive ? 'var(--text-primary)' : 'var(--text-secondary)',
        };
    }
  };

  const containerStyle: React.CSSProperties = {
    display: 'flex',
    alignItems: 'center',
    gap: variant === 'underline' ? 0 : '0.25rem',
    padding: variant === 'underline' ? 0 : '0.25rem',
    background: variant === 'underline' ? 'transparent' : 'var(--bg-secondary)',
    borderRadius: variant === 'underline' ? 0 : '8px',
    border:
      variant === 'underline' ? 'none' : '1px solid var(--border-primary)',
    borderBottom:
      variant === 'underline' ? '1px solid var(--border-primary)' : undefined,
    overflowX: 'auto',
  };

  return (
    <div style={containerStyle}>
      {tabs.map((tab) => {
        const isActive = activeTab === tab.id;
        return (
          <button
            key={tab.id}
            onClick={() => onChange(tab.id)}
            style={{
              ...baseTabStyle,
              ...getVariantStyles(isActive),
            }}
            onMouseEnter={(e) => {
              if (!isActive) {
                e.currentTarget.style.color = 'var(--text-primary)';
                if (variant !== 'underline') {
                  e.currentTarget.style.background = 'var(--bg-hover)';
                }
              }
            }}
            onMouseLeave={(e) => {
              if (!isActive) {
                e.currentTarget.style.color = 'var(--text-secondary)';
                if (variant !== 'underline') {
                  e.currentTarget.style.background = 'transparent';
                }
              }
            }}>
            {tab.icon && <span style={{ display: 'flex' }}>{tab.icon}</span>}
            <span>{tab.label}</span>
            {tab.count !== undefined && (
              <span
                style={{
                  background: isActive
                    ? 'var(--brand-primary)'
                    : 'var(--bg-tertiary)',
                  color: isActive ? 'white' : 'var(--text-secondary)',
                  fontSize: '0.6875rem',
                  fontWeight: 600,
                  padding: '0.125rem 0.5rem',
                  borderRadius: '10px',
                  minWidth: '20px',
                  textAlign: 'center',
                }}>
                {tab.count}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

// Tab Panel component for content
interface TabPanelProps {
  children: ReactNode;
  tabId: string;
  activeTab: string;
}

export function TabPanel({ children, tabId, activeTab }: TabPanelProps) {
  if (tabId !== activeTab) {
    return null;
  }

  return <div style={{ marginTop: '1rem' }}>{children}</div>;
}

// Filter Tabs - horizontal pills for filtering (like s issue filters)
interface FilterTabsProps {
  filters: Array<{
    id: string;
    label: string;
    count?: number;
    color?: 'default' | 'success' | 'warning' | 'error';
  }>;
  activeFilter: string;
  onChange: (filterId: string) => void;
}

export function FilterTabs({
  filters,
  activeFilter,
  onChange,
}: FilterTabsProps) {
  const getColorStyles = (
    color: 'default' | 'success' | 'warning' | 'error' = 'default',
    isActive: boolean,
  ) => {
    const colors = {
      default: {
        bg: isActive ? 'var(--brand-primary-light)' : 'transparent',
        border: isActive
          ? 'var(--brand-primary-border)'
          : 'var(--border-primary)',
        text: isActive ? 'var(--brand-primary)' : 'var(--text-secondary)',
        countBg: isActive ? 'var(--brand-primary)' : 'var(--bg-tertiary)',
        countText: isActive ? 'white' : 'var(--text-secondary)',
      },
      success: {
        bg: isActive ? 'var(--status-success-bg)' : 'transparent',
        border: isActive
          ? 'var(--status-success-border)'
          : 'var(--border-primary)',
        text: isActive ? 'var(--status-success)' : 'var(--text-secondary)',
        countBg: isActive ? 'var(--status-success)' : 'var(--bg-tertiary)',
        countText: isActive ? 'white' : 'var(--text-secondary)',
      },
      warning: {
        bg: isActive ? 'var(--status-warning-bg)' : 'transparent',
        border: isActive
          ? 'var(--status-warning-border)'
          : 'var(--border-primary)',
        text: isActive ? 'var(--status-warning)' : 'var(--text-secondary)',
        countBg: isActive ? 'var(--status-warning)' : 'var(--bg-tertiary)',
        countText: isActive ? 'white' : 'var(--text-secondary)',
      },
      error: {
        bg: isActive ? 'var(--status-error-bg)' : 'transparent',
        border: isActive
          ? 'var(--status-error-border)'
          : 'var(--border-primary)',
        text: isActive ? 'var(--status-error)' : 'var(--text-secondary)',
        countBg: isActive ? 'var(--status-error)' : 'var(--bg-tertiary)',
        countText: isActive ? 'white' : 'var(--text-secondary)',
      },
    };
    return colors[color];
  };

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '0.5rem',
        flexWrap: 'wrap',
      }}>
      {filters.map((filter) => {
        const isActive = activeFilter === filter.id;
        const colors = getColorStyles(filter.color, isActive);
        return (
          <button
            key={filter.id}
            onClick={() => onChange(filter.id)}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '0.5rem',
              padding: '0.375rem 0.75rem',
              fontSize: '0.8125rem',
              fontWeight: 500,
              cursor: 'pointer',
              border: `1px solid ${colors.border}`,
              background: colors.bg,
              color: colors.text,
              borderRadius: '6px',
              transition: 'all 0.15s ease',
            }}>
            <span>{filter.label}</span>
            {filter.count !== undefined && (
              <span
                style={{
                  background: colors.countBg,
                  color: colors.countText,
                  fontSize: '0.6875rem',
                  fontWeight: 600,
                  padding: '0.125rem 0.5rem',
                  borderRadius: '10px',
                  minWidth: '20px',
                  textAlign: 'center',
                }}>
                {filter.count}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

export default Tabs;
