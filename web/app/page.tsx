"use client";

import { useCallback, useEffect, useState } from "react";

import { StatusBadge } from "@/components/status-badge";
import { api, type Health, type Signal } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

/** 总览页：系统状态、熔断控制、最新信号。 */
export default function DashboardPage() {
  const { t, formatCurrency, formatPercent, formatNumber } = useI18n();
  const [health, setHealth] = useState<Health | null>(null);
  const [signals, setSignals] = useState<Signal[]>([]);
  const [error, setError] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [announce, setAnnounce] = useState<string>("");

  const load = useCallback(async () => {
    try {
      setError("");
      const h = await api.health();
      setHealth(h);
      const s = await api.signals(10).catch(() => ({ count: 0, signals: [] as Signal[] }));
      setSignals(s.signals ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), 15_000);
    return () => clearInterval(timer);
  }, [load]);

  const togglePause = async (pause: boolean) => {
    setBusy(true);
    try {
      if (pause) await api.pause(t("risk.pauseReason"), 60);
      else await api.resume();
      setAnnounce(pause ? t("risk.paused") : t("risk.resumed"));
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold text-white">{t("nav.overview")}</h1>

      {/* 状态播报区（屏幕阅读器） */}
      <p aria-live="polite" className="sr-only">
        {announce}
      </p>

      {error && (
        <div role="alert" className="card border-danger/40 text-sm text-danger">
          {t("health.unavailable", { base: api.base, error })}
        </div>
      )}

      <section aria-labelledby="system-status">
        <h2 id="system-status" className="mb-3 text-lg font-medium text-white">
          {t("health.sectionTitle")}
        </h2>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Stat
            label={t("health.mode")}
            value={health?.mode ?? "—"}
            hint={health?.dry_run ? t("health.modeDryRun") : t("health.modeLive")}
          />
          <Stat
            label={t("health.chains")}
            value={formatNumber(health?.chains?.length ?? 0)}
            hint={health?.chains?.join(" · ") ?? "—"}
          />
          <Stat
            label={t("health.database")}
            value={health?.database ? t("health.databaseOk") : t("health.databaseDown")}
            hint={health?.database ? t("health.databaseOkHint") : t("health.databaseDownHint")}
          />
          <Stat
            label={t("health.signer")}
            value={health?.signer ? t("health.signerOk") : t("health.signerEmpty")}
            hint={health?.signer ? t("health.signerOkHint") : t("health.signerEmptyHint")}
          />
        </div>
      </section>

      <section className="card space-y-3" aria-labelledby="risk-control">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 id="risk-control" className="text-lg font-medium text-white">
            {t("risk.title")}
          </h2>
          <div className="flex gap-2">
            <button
              type="button"
              className="btn"
              disabled={busy}
              aria-busy={busy}
              onClick={() => void togglePause(true)}
            >
              {t("risk.pause")}
            </button>
            <button
              type="button"
              className="btn"
              disabled={busy}
              aria-busy={busy}
              onClick={() => void togglePause(false)}
            >
              {t("risk.resume")}
            </button>
          </div>
        </div>
        <p className="text-sm text-slate-400">{t("risk.description")}</p>
        {health?.alerts && Object.keys(health.alerts).length > 0 && (
          <p className="text-xs text-slate-500">
            {t("health.alertChannels")}:{" "}
            {Object.entries(health.alerts)
              .map(([name, state]) => `${name} = ${state}`)
              .join(" · ")}
          </p>
        )}
      </section>

      <section className="card" aria-labelledby="latest-signals">
        <h2 id="latest-signals" className="mb-3 text-lg font-medium text-white">
          {t("signals.title")}
        </h2>
        {signals.length === 0 ? (
          <p className="text-sm text-slate-500">{t("signals.emptyHint")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <caption className="sr-only">{t("signals.title")}</caption>
              <thead className="text-left text-slate-400">
                <tr>
                  <th scope="col" className="py-2">
                    {t("common.token")}
                  </th>
                  <th scope="col">{t("signals.source")}</th>
                  <th scope="col" className="text-right">
                    {t("common.amountUsd")}
                  </th>
                  <th scope="col" className="text-right">
                    {t("common.liquidity")}
                  </th>
                  <th scope="col">{t("common.status")}</th>
                  <th scope="col" className="text-right">
                    {t("signals.decay")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {signals.map((s) => (
                  <tr key={s.id} className="border-t border-slate-800">
                    <td className="py-2 font-mono text-xs" title={s.token}>
                      {shorten(s.token)}
                    </td>
                    <td>{sourceLabel(s.source, t)}</td>
                    <td className="text-right tabular-nums">{formatCurrency(s.amount_usd)}</td>
                    <td className="text-right tabular-nums">{formatCurrency(s.liquidity_usd)}</td>
                    <td>
                      <StatusBadge status={s.status} />
                    </td>
                    <td className="text-right tabular-nums">{formatPercent(s.decay, 0)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="card">
      <p className="text-xs uppercase tracking-wide text-slate-500">{label}</p>
      <p className="mt-1 text-xl font-semibold text-white">{value}</p>
      {hint && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

/** 信号来源本地化（page 文件只允许 default 导出，故此处不对外导出）。 */
function sourceLabel(source: string, t: (k: string) => string): string {
  switch (source) {
    case "follow":
      return t("signals.sourceFollow");
    case "autonomous":
      return t("signals.sourceAutonomous");
    case "agent":
      return t("signals.sourceAgent");
    default:
      return source;
  }
}

/** 地址/哈希缩写（保留首尾，避免表格换行）。 */
function shorten(value: string) {
  if (!value) return "—";
  return value.length > 14 ? `${value.slice(0, 6)}…${value.slice(-4)}` : value;
}
