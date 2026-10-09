import {
  BrowserRouter as Router,
  Routes,
  Route,
  Navigate,
  useParams,
} from 'react-router-dom';
import { useEffect, lazy, Suspense } from 'react';
import { DateRangeProvider } from './contexts/DateRangeProvider';
import { useAuth } from './contexts/AuthContext';
import { enterDemoMode, DEMO_ORG_SLUG } from './demo/demoMode';
import ProtectedRoute from './components/ProtectedRoute';
import { DemoBanner } from './components/DemoBanner';
import OrgSlugGuard from './components/OrgSlugGuard';
import { EmailVerificationGate } from './components/EmailVerificationGate';
import { MfaEnrollmentGate } from './components/MfaEnrollmentGate';
import { Sidebar } from './components/Sidebar';
import { ErrorBoundary } from './components/ErrorBoundary';
import { PlanOverLimitBanner } from './components/PlanOverLimitBanner';
import { TrialBanner } from './components/TrialBanner';
import { IngestLimitBanner } from './components/IngestLimitBanner';

// Views load on demand: a visitor of the landing page or the demo only
// downloads the views they open. HomeView stays in the entry chunk.
const OverviewView = lazy(() => import('./views/OverviewView'));
const ErrorsView = lazy(() => import('./views/ErrorsView'));
const ServicesView = lazy(() => import('./views/ServicesView'));
const ServiceDetailView = lazy(() => import('./views/ServiceDetailView'));
const HostsView = lazy(() => import('./views/HostsView'));
const HostGroupsView = lazy(() => import('./views/HostGroupsView'));
const HostDetailView = lazy(() => import('./views/HostDetailView'));
const TimelineView = lazy(() => import('./views/TimelineView'));
const LoginView = lazy(() => import('./views/LoginView'));
const CreateOrganizationView = lazy(() => import('./views/CreateOrganizationView'));
const VerifyEmailView = lazy(() => import('./views/VerifyEmailView'));
const AcceptInviteView = lazy(() => import('./views/AcceptInviteView'));
const ResetPasswordView = lazy(() => import('./views/ResetPasswordView'));
const ContactView = lazy(() => import('./views/ContactView'));
const UnsubscribeView = lazy(() => import('./views/UnsubscribeView'));
const SettingsView = lazy(() => import('./views/SettingsView'));
const AccountView = lazy(() => import('./views/AccountView'));
const DashboardsView = lazy(() => import('./views/DashboardsView'));
const TracesView = lazy(() => import('./views/TracesView'));
const MetricsExplorerView = lazy(() => import('./views/MetricsExplorerView'));
const NetworkView = lazy(() => import('./views/NetworkView'));
const LogsView = lazy(() => import('./views/LogsView'));
const NotificationChannelsView = lazy(() => import('./views/NotificationChannelsView'));
const AlertRulesView = lazy(() => import('./views/AlertRulesView'));
const IncidentsView = lazy(() => import('./views/IncidentsView'));
const ProfilingView = lazy(() => import('./views/ProfilingView'));
const MaintenanceView = lazy(() => import('./views/MaintenanceView'));
import HomeView from './views/HomeView';
const DocumentationView = lazy(() => import('./views/DocumentationView'));
const PricingView = lazy(() => import('./views/PricingView'));
const StatusPageView = lazy(() => import('./views/StatusPageView'));
const LegalView = lazy(() => import('./views/LegalView'));
const ComparisonView = lazy(() => import('./views/ComparisonView'));
const PlatformAdminView = lazy(() => import('./views/PlatformAdminView'));
import comparisons from './seo/comparisons.json';
import ServiceModal from './components/ServiceModal';

import './App.css';

function App() {
  return (
    <Router>
      <ErrorBoundary>
        <Suspense fallback={<RouteFallback />}>
        <Routes>
          {/* Public routes */}
          <Route
            path='/login'
            element={
              <PublicRoute>
                <LoginView />
              </PublicRoute>
            }
          />
          <Route
            path='/register'
            element={<Navigate to='/get-started' replace />}
          />
          <Route
            path='/get-started'
            element={
              <PublicRoute>
                <CreateOrganizationView />
              </PublicRoute>
            }
          />
          {/* Public: email confirmation landing page (reachable from the email link) */}
          <Route path='/verify-email' element={<VerifyEmailView />} />
          {/* Public: invited user sets their password to activate the account */}
          <Route path='/accept-invite' element={<AcceptInviteView />} />
          {/* Public: request a reset link, or set a new password from one */}
          <Route path='/reset-password' element={<ResetPasswordView />} />

          {/* Landing / home page (s-style) */}
          <Route path='/' element={<HomeView />} />
          {/* Live demo: activates the frontend-only demo session then reloads into the demo org */}
          <Route path='/demo' element={<DemoEntry />} />
          {/* Documentation */}
          <Route path='/docs' element={<DocumentationView />} />
          {/* Pricing */}
          <Route path='/pricing' element={<PricingView />} />
          {/* Public status page: Middle Monitor's own availability */}
          <Route path='/status' element={<StatusPageView />} />
          {/* Contact Us */}
          <Route path='/contact' element={<ContactView />} />

          {/* Opt-out target of outreach emails: public, unauthenticated, never indexed */}
          <Route path='/unsub' element={<UnsubscribeView />} />
          {/* Competitor comparison pages */}
          <Route path='/alternatives' element={<ComparisonRoute />} />
          <Route path='/alternatives/:slug' element={<ComparisonRoute />} />
          {/* Legal pages */}
          <Route path='/legal' element={<LegalView page='notice' />} />
          <Route path='/privacy' element={<LegalView page='privacy' />} />
          <Route path='/terms' element={<LegalView page='terms' />} />

          {/* Cross-org admin page (instance owner only; PlatformAdminView itself
              gates on isPlatformAdmin, the backend is the real boundary). */}
          <Route
            path='/platform-admin'
            element={
              <ProtectedRoute>
                <PlatformAdminView />
              </ProtectedRoute>
            }
          />

          {/* Org-scoped protected routes */}
          <Route
            path='/organizations/:orgSlug/*'
            element={
              <ProtectedRoute>
                <EmailVerificationGate>
                  <MfaEnrollmentGate>
                    <OrgSlugGuard>
                      <DateRangeProvider>
                        <div className='app'>
                          <Sidebar />
                          <main className='main-content'>
                            <div className='content-wrapper'>
                              <DemoBanner />
                              <TrialBanner />
                              <PlanOverLimitBanner />
                              <IngestLimitBanner />
                              <ErrorBoundary>
                                {/* Inner boundary: the sidebar stays put while a view loads. */}
                                <Suspense fallback={null}>
                                <Routes>
                                  {/* Dashboards */}
                                  <Route index element={<OverviewView />} />
                                  <Route
                                    path='dashboards'
                                    element={<DashboardsView />}
                                  />

                                  {/* APM */}
                                  <Route
                                    path='errors'
                                    element={<ErrorsView />}
                                  />
                                  <Route
                                    path='services'
                                    element={<ServicesView />}
                                  />
                                  <Route
                                    path='services/:id'
                                    element={<ServiceDetailView />}
                                  />
                                  <Route
                                    path='traces'
                                    element={<TracesView />}
                                  />

                                  {/* Infrastructure */}
                                  <Route path='hosts' element={<HostsView />} />
                                  <Route
                                    path='hosts/groups'
                                    element={<HostGroupsView />}
                                  />
                                  <Route
                                    path='hosts/:id'
                                    element={<HostDetailView />}
                                  />
                                  <Route
                                    path='metrics'
                                    element={<MetricsExplorerView />}
                                  />
                                  <Route
                                    path='profiling'
                                    element={<ProfilingView />}
                                  />
                                  <Route
                                    path='network'
                                    element={<NetworkView />}
                                  />

                                  {/* Events & Logs */}
                                  <Route
                                    path='timeline'
                                    element={<TimelineView />}
                                  />
                                  <Route path='logs' element={<LogsView />} />

                                  {/* Alerts */}
                                  <Route
                                    path='alerts/incidents'
                                    element={<IncidentsView />}
                                  />
                                  <Route
                                    path='alerts/rules'
                                    element={<AlertRulesView />}
                                  />
                                  <Route
                                    path='alerts/channels'
                                    element={<NotificationChannelsView />}
                                  />
                                  <Route
                                    path='alerts/maintenance'
                                    element={<MaintenanceView />}
                                  />

                                  {/* Personal account settings */}
                                  <Route
                                    path='account'
                                    element={<AccountView />}
                                  />
                                  {/* Organization settings (org tokens + agent tokens live here now) */}
                                  <Route
                                    path='settings'
                                    element={<SettingsView />}
                                  />
                                  {/* Legacy: API keys are now inside org settings. */}
                                  <Route
                                    path='api-keys'
                                    element={
                                      <Navigate to='../settings' replace />
                                    }
                                  />
                                </Routes>
                                </Suspense>
                              </ErrorBoundary>
                            </div>
                          </main>
                          <ServiceModal />
                        </div>
                      </DateRangeProvider>
                    </OrgSlugGuard>
                  </MfaEnrollmentGate>
                </EmailVerificationGate>
              </ProtectedRoute>
            }
          />

          {/* Catch-all: redirect to landing */}
          <Route path='*' element={<Navigate to='/' replace />} />
        </Routes>
        </Suspense>
      </ErrorBoundary>
    </Router>
  );
}

function RouteFallback() {
  return (
    <div className='loading-screen'>
      <div className='loading-spinner' />
    </div>
  );
}

// Sets the demo flag then hard-reloads into the demo org, so AuthContext and
// every provider re-initialize with the fake session.
function DemoEntry() {
  useEffect(() => {
    enterDemoMode();
    window.location.replace(`/organizations/${DEMO_ORG_SLUG}`);
  }, []);

  return (
    <div className='loading-screen'>
      <div className='loading-spinner' />
      <p>Loading...</p>
    </div>
  );
}

// An unknown competitor slug falls back to the index rather than 404, so a
// stale inbound link still lands on indexable content.
function ComparisonRoute() {
  const { slug } = useParams<{ slug: string }>();
  const known = comparisons.pages.some((p) => p.slug === slug);
  if (slug && !known) return <Navigate to='/alternatives' replace />;
  return <ComparisonView slug={slug} />;
}

// Redirect authenticated users away from login/register
function PublicRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, isLoading, organization } = useAuth();

  if (isLoading) {
    return (
      <div className='loading-screen'>
        <div className='loading-spinner' />
        <p>Loading...</p>
      </div>
    );
  }

  if (isAuthenticated) {
    const slug = organization?.slug || 'default';
    return <Navigate to={`/organizations/${slug}`} replace />;
  }

  return <>{children}</>;
}

export default App;
