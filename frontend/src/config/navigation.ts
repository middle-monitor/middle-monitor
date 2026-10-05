import { IconType } from 'react-icons';
import {
  HiOutlineChartPie,
  HiOutlineSquares2X2,
  HiOutlineExclamationTriangle,
  HiOutlineCube,
  HiOutlineSignal,
  HiOutlineServerStack,
  HiOutlineChartBar,
  HiOutlineGlobeAlt,
  HiOutlineClock,
  HiOutlineDocumentText,
  HiOutlineBell,
  HiOutlineBellAlert,
  HiOutlineShieldCheck,
  HiOutlineCog6Tooth,
  HiOutlineWrenchScrewdriver,
  HiOutlineUserCircle,
  HiOutlineRectangleGroup,
  HiOutlinePresentationChartLine,
} from 'react-icons/hi2';

export interface NavItem {
  id: string;
  /** i18n key resolved via t() in the sidebar; falls back to the key if missing. */
  labelKey: string;
  icon: IconType;
  path?: string;
  /** Absolute path (no org prefix) - e.g. /docs for documentation */
  externalPath?: string;
  badge?: 'new' | 'beta' | 'coming-soon' | 'pro';
  children?: NavItem[];
  disabled?: boolean;
  /** Only rendered for org admins (e.g. org settings). */
  adminOnly?: boolean;
}

export interface NavSection {
  id: string;
  /** i18n key for the section header. */
  labelKey: string;
  items: NavItem[];
  defaultExpanded?: boolean;
  separatorBefore?: boolean;
}

export const navigationConfig: NavSection[] = [
  {
    id: 'dashboards',
    labelKey: 'sidebar.sections.dashboards',
    defaultExpanded: true,
    items: [
      {
        id: 'overview',
        labelKey: 'sidebar.items.overview',
        icon: HiOutlineChartPie,
        path: '/',
      },
      {
        id: 'custom-dashboards',
        labelKey: 'sidebar.items.custom_dashboards',
        icon: HiOutlineSquares2X2,
        path: '/dashboards',
      },
    ],
  },
  {
    id: 'synthetics',
    labelKey: 'sidebar.sections.uptime',
    defaultExpanded: true,
    items: [
      {
        id: 'services',
        labelKey: 'sidebar.items.services',
        icon: HiOutlineCube,
        path: '/services',
      },
    ],
  },
  {
    id: 'infrastructure',
    labelKey: 'sidebar.sections.infrastructure',
    defaultExpanded: true,
    items: [
      {
        id: 'host-groups',
        labelKey: 'sidebar.items.host_groups',
        icon: HiOutlineRectangleGroup,
        path: '/hosts/groups',
      },
      {
        id: 'hosts',
        labelKey: 'sidebar.items.hosts',
        icon: HiOutlineServerStack,
        path: '/hosts',
      },
      {
        id: 'network',
        labelKey: 'sidebar.items.network',
        icon: HiOutlineGlobeAlt,
        path: '/network',
      },
      {
        id: 'metrics',
        labelKey: 'sidebar.items.metrics',
        icon: HiOutlinePresentationChartLine,
        path: '/metrics',
      },
    ],
  },
  {
    id: 'apm',
    labelKey: 'sidebar.sections.apm',
    defaultExpanded: true,
    items: [
      {
        id: 'traces',
        labelKey: 'sidebar.items.traces',
        icon: HiOutlineSignal,
        path: '/traces',
      },
      {
        id: 'profiling',
        labelKey: 'sidebar.items.profiling',
        icon: HiOutlineChartBar,
        path: '/profiling',
        badge: 'pro' as const,
      },
      {
        id: 'errors',
        labelKey: 'sidebar.items.errors',
        icon: HiOutlineExclamationTriangle,
        path: '/errors',
      },
      {
        id: 'logs',
        labelKey: 'sidebar.items.logs',
        icon: HiOutlineDocumentText,
        path: '/logs',
      },
    ],
  },
  {
    id: 'events',
    labelKey: 'sidebar.sections.events',
    defaultExpanded: true,
    items: [
      {
        id: 'timeline',
        labelKey: 'sidebar.items.timeline',
        icon: HiOutlineClock,
        path: '/timeline',
      },
    ],
  },
  {
    id: 'alerts',
    labelKey: 'sidebar.sections.ops',
    defaultExpanded: true,
    items: [
      {
        id: 'incidents',
        labelKey: 'sidebar.items.incidents',
        icon: HiOutlineShieldCheck,
        path: '/alerts/incidents',
      },
      {
        id: 'rules',
        labelKey: 'sidebar.items.rules',
        icon: HiOutlineBellAlert,
        path: '/alerts/rules',
        badge: 'pro' as const,
      },
      {
        id: 'channels',
        labelKey: 'sidebar.items.channels',
        icon: HiOutlineBell,
        path: '/alerts/channels',
        badge: 'pro' as const,
      },
      {
        id: 'maintenance',
        labelKey: 'sidebar.items.maintenance',
        icon: HiOutlineWrenchScrewdriver,
        path: '/alerts/maintenance',
        badge: 'pro' as const,
      },
    ],
  },
  {
    id: 'organization',
    labelKey: 'sidebar.sections.organization',
    defaultExpanded: true,
    separatorBefore: true,
    items: [
      {
        id: 'docs',
        labelKey: 'sidebar.items.docs',
        icon: HiOutlineDocumentText,
        externalPath: '/docs',
      },
      {
        id: 'account',
        labelKey: 'sidebar.items.account',
        icon: HiOutlineUserCircle,
        path: '/account',
      },
      {
        id: 'settings',
        labelKey: 'sidebar.items.organization',
        icon: HiOutlineCog6Tooth,
        path: '/settings',
        adminOnly: true,
      },
    ],
  },
];
