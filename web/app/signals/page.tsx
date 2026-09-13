"use client";

import { useCallback, useEffect, useState } from "react";

import { StatusBadge } from "@/components/status-badge";
import { useRealtime } from "@/hooks/use-realtime";
import { api, type Signal } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

/** 信号流页面：自动刷新、来源/状态本地化、可访问表格。 */
export default function SignalsPage() {
  const { t, formatCurrency, formatPercent, formatDateTime, formatRelativeTime } = useI18n();
  const [signals, setSignals] = useState<Signal[]>([]);
  const [error, setError] = useState("");
  const [auto, setAuto] = useState(true);
  const [loading, setLoading] = useState(false);

  // 实时推送：服务端检测到信号新增/状态变化即推送，前端无需等待轮询
  const { connected } = useRealtime((evt) => {
    if (evt.type !== "signal" || !evt.data) return;
    const incoming = evt.data as Signal;
    setSignals((prev) => {
      const idx = prev.findIndex((s) => s.id === incoming.id);
      if (idx === -1) return [incoming, ...prev].slice(0, 200);
      const next = prev.slice();
      next[idx] = incoming;
      return next;
    });
  });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setError("");
      const res = await api.signals(100);
      setSignals(res.signals ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    if (!auto) return;
    const timer = setInterval(() => void load(), 10_000);
    return () => clearInterval(timer);
  }, [load, auto]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-semibold text-white">{t("signals.title")}</h1>
        <span
          className={`badge ${connected ? "bg-ok/20 text-ok" : "bg-slate-700 text-slate-300"}`}
          title={t("realtime.label")}
          aria-live="polite"
        >
          {connected ? t("realtime.live") : t("realtime.polling")}
        </span>
        <button type="button" className="btn" onClick={() => void load()} aria-busy={loading}>
          {t("common.refresh")}
        </button>
        <label className="ml-auto flex items-center gap-2 text-sm text-slate-400">
          <input
            type="checkbox"
            checked={auto}
            onChange={(e) => setAuto(e.target.checked)}
            className="h-4 w-4 rounded border-slate-600 bg-surface focus-visible:ring-2 focus-visible:ring-accent"
          />
          {t("signals.autoRefresh")}
        </label>
      </div>

      {error && (
        <div role="alert" className="card border-danger/40 text-sm text-danger">
          {error}
        </div>
      )}

      {signals.length === 0 ? (
        <div className="card text-sm text-slate-500">{t("signals.emptyHint")}</div>
      ) : (
        <div className="card overflow-x-auto">
          <table className="w-full text-sm">
            <caption className="sr-only">{t("signals.title")}</caption>
            <thead className="text-left text-slate-400">
              <tr>
                <th scope="col" className="py-2">
                  {t("common.time")}
                </th>
                <th scope="col">{t("common.chain")}</th>
                <th scope="col">{t("common.token")}</th>
                <th scope="col">{t("signals.source")}</th>
                <th scope="col">{t("signals.triggerAddress")}</th>
                <th scope="col" className="text-right">
                  {t("common.amountUsd")}
                </th>
                <th scope="col" className="text-right">
                  {t("common.price")}
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
                  <td className="py-2 text-xs text-slate-400" title={formatDateTime(s.created_at)}>
                    {formatRelativeTime(s.created_at)}
                  </td>
                  <td>{s.chain}</td>
                  <td className="font-mono text-xs" title={s.token}>
                    {shorten(s.token)}
                  </td>
                  <td>{sourceLabel(s.source, t)}</td>
                  <td className="font-mono text-xs text-slate-400" title={s.trigger_address}>
                    {shorten(s.trigger_address ?? "")}
                  </td>
                  <td className="text-right tabular-nums">{formatCurrency(s.amount_usd)}</td>
                  <td className="text-right tabular-nums">{formatCurrency(s.price_usd)}</td>
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
    </div>
  );
}

/** 信号来源本地化（非枚举值原样返回，便于后端扩展时降级显示）。 */
function sourceLabel(source: string, t: (key: string) => string): string {
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
