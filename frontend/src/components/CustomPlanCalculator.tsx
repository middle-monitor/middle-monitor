import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAuth } from '../contexts/AuthContext';
import { useOrgApi } from '../hooks/useOrgApi';
import { usePlan } from './PlanGate';
import './CustomPlanCalculator.css';

const RETENTION_COSTS = [
  { days: 30, extra: 0 },
  { days: 60, extra: 8 },
  { days: 90, extra: 18 },
  { days: 365, extra: 35 },
];

// Custom is priced from the Pro plan plus per-unit increments for whatever you
// add beyond the Pro baseline. This keeps the Pro -> Custom transition continuous
// (no price jump) and coherent with what Pro already includes.
export const PRO_BASE_PRICE = 49;
export const PRO_HOSTS = 10;
export const PRO_SERVICES = 50;
export const PRO_SDK = 10;
const HOST_UNIT = 3;
const SERVICE_UNIT = 1;
const SDK_UNIT = 3;

export interface CustomPlanValues {
  hosts: number;
  services: number;
  sdk_services: number;
  retention_days: number;
  total: number;
}

interface CustomPlanCalculatorProps {
  onValuesChange: (values: CustomPlanValues) => void;
}

// Interactive hosts/services/sdk/retention sliders that price a custom plan.
// For an existing paid admin, pre-fills from their live subscription so
// adjusting starts from what they actually have, not generic defaults.
export function CustomPlanCalculator({ onValuesChange }: CustomPlanCalculatorProps) {
  const { t, i18n } = useTranslation();
  const { isAdmin } = useAuth();
  const { isPaidSubscriber } = usePlan();
  const orgApi = useOrgApi();

  const [hosts, setHosts] = useState(15);
  const [services, setServices] = useState(80);
  const [sdk, setSdk] = useState(10);
  const [retention, setRetention] = useState(30);

  useEffect(() => {
    if (!isPaidSubscriber || !isAdmin) return;
    let cancelled = false;
    orgApi.payments.getSubscription()
      .then((res) => {
        if (cancelled || !res.data) return;
        setHosts(res.data.hosts);
        setServices(res.data.services);
        setSdk(res.data.sdk_services);
        setRetention(res.data.retention_days);
      })
      .catch(() => { /* keep defaults */ });
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isPaidSubscriber, isAdmin]);

  const retentionExtra = RETENTION_COSTS.find(r => r.days === retention)?.extra ?? 0;
  const extraHostsCost = Math.max(0, hosts - PRO_HOSTS) * HOST_UNIT;
  const extraServicesCost = Math.max(0, services - PRO_SERVICES) * SERVICE_UNIT;
  const extraSdkCost = Math.max(0, sdk - PRO_SDK) * SDK_UNIT;

  const total = useMemo(
    () => Math.round(PRO_BASE_PRICE + extraHostsCost + extraServicesCost + extraSdkCost + retentionExtra),
    [extraHostsCost, extraServicesCost, extraSdkCost, retentionExtra],
  );

  useEffect(() => {
    onValuesChange({ hosts, services, sdk_services: sdk, retention_days: retention, total });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hosts, services, sdk, retention, total]);

  const retentionOptions = [
    { days: 30, label: t('pricing.calculator.btn_30d') },
    { days: 60, label: t('pricing.calculator.btn_60d') },
    { days: 90, label: t('pricing.calculator.btn_90d') },
    { days: 365, label: t('pricing.calculator.btn_1yr') },
  ];

  return (
    <div className='plan-calculator'>
      {/* Hosts slider */}
      <div className='plan-calculator-row'>
        <div className='plan-calculator-row-header'>
          <span className='plan-calculator-label'>{t('pricing.calculator.label_hosts')}</span>
          <span className='plan-calculator-val'>{hosts}</span>
        </div>
        <input
          type='range'
          min={11}
          max={200}
          step={1}
          value={hosts}
          onChange={e => setHosts(Number(e.target.value))}
          className='plan-calculator-slider'
        />
        <div className='plan-calculator-slider-bounds'><span>11</span><span>200</span></div>
        {/* The ingestion budget is 250 points/min per host, so it follows this slider. */}
        <p className='plan-calculator-note'>
          {t('pricing.calculator.points_included', { points: (hosts * 250).toLocaleString(i18n.language) })}
        </p>
      </div>

      {/* Services slider */}
      <div className='plan-calculator-row'>
        <div className='plan-calculator-row-header'>
          <span className='plan-calculator-label'>{t('pricing.calculator.label_services')}</span>
          <span className='plan-calculator-val'>{services}</span>
        </div>
        <input
          type='range'
          min={50}
          max={500}
          step={5}
          value={services}
          onChange={e => setServices(Number(e.target.value))}
          className='plan-calculator-slider'
        />
        <div className='plan-calculator-slider-bounds'><span>50</span><span>500</span></div>
      </div>

      {/* SDK services slider (errors, traces, logs, profiling) */}
      <div className='plan-calculator-row'>
        <div className='plan-calculator-row-header'>
          <span className='plan-calculator-label'>{t('pricing.calculator.label_sdk')}</span>
          <span className='plan-calculator-val'>{sdk}</span>
        </div>
        <input
          type='range'
          min={10}
          max={100}
          step={1}
          value={sdk}
          onChange={e => setSdk(Number(e.target.value))}
          className='plan-calculator-slider'
        />
        <div className='plan-calculator-slider-bounds'><span>10</span><span>100</span></div>
      </div>

      {/* Retention selector */}
      <div className='plan-calculator-row'>
        <div className='plan-calculator-row-header'>
          <span className='plan-calculator-label'>{t('pricing.calculator.label_retention')}</span>
        </div>
        <div className='plan-calculator-retention-tabs'>
          {retentionOptions.map((opt, i) => {
            const extra = RETENTION_COSTS[i].extra;
            return (
              <button
                key={opt.days}
                type='button'
                className={`plan-calculator-retention-tab${retention === opt.days ? ' plan-calculator-retention-tab-active' : ''}`}
                onClick={() => setRetention(opt.days)}
              >
                {opt.label}
                {extra > 0 && <span className='plan-calculator-retention-extra'>+€{extra}</span>}
              </button>
            );
          })}
        </div>
      </div>

      {/* Breakdown — Pro base + increments above the Pro plan. */}
      <div className='plan-calculator-breakdown'>
        <div className='plan-calculator-breakdown-row'>
          <span>{t('pricing.calculator.base_pro')}</span>
          <span>€{PRO_BASE_PRICE}</span>
        </div>
        {extraHostsCost > 0 && (
          <div className='plan-calculator-breakdown-row'>
            <span>+{hosts - PRO_HOSTS} hosts × €{HOST_UNIT}</span>
            <span>+€{extraHostsCost}</span>
          </div>
        )}
        {extraServicesCost > 0 && (
          <div className='plan-calculator-breakdown-row'>
            <span>+{services - PRO_SERVICES} services × €{SERVICE_UNIT}</span>
            <span>+€{extraServicesCost}</span>
          </div>
        )}
        {extraSdkCost > 0 && (
          <div className='plan-calculator-breakdown-row'>
            <span>+{sdk - PRO_SDK} SDK × €{SDK_UNIT}</span>
            <span>+€{extraSdkCost}</span>
          </div>
        )}
        {retentionExtra > 0 && (
          <div className='plan-calculator-breakdown-row'>
            <span>{t('pricing.calculator.label_retention')}</span>
            <span>+€{retentionExtra}</span>
          </div>
        )}
      </div>
    </div>
  );
}
