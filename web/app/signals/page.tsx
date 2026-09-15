"use client";

import { useCallback, useEffect, useMemo, useState } from "react";

import { QuickTrade } from "@/components/quick-trade";
import { RiskLight, assessRisk } from "@/components/risk-light";
import { StatusBadge } from "@/components/status-badge";
import { useRealtime } from "@/hooks/use-realtime";
import { api, type Signal } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

type ViewMode = "cards" | "table";
type SortKey = "newest" | "amount" | "liquidity";

/**
 * 发现流（交易台主区域）：实时推送 + 筛选 + 卡片/表格切换 + 点击展开快捷交易。
 *
 * 交互取舍：
 *   - 风险灯常驻每一条（买入前风险默认可见，不能一键无提示）；
 *   - 实时事件到达时插入顶部，列表上限 200 条，避免长时间运行内存增长；
 *   - 筛选与排序在客户端进行，刷新不丢失当前上下文。
 */
export default function SignalsPage() {
  const { t, formatCurrency, formatPercent, formatDateTime, formatRelativeTime } = useI18n();
  const [signals, setSignals] = useState<Signal[]>([]);
  const [error, setError] = useState("");
  const [auto, setAuto] = useState(true);
  const [loading, setLoading] = useState(false);
  const [view, setView] = useState<ViewMode>("cards");
  const [sort, setSort] = useState<SortKey>("newest");
  const [onlyVerified, setOnlyVerified] = useState(false);
  const [excludeRisky, setExcludeRisky] = useState(false);
  const [minLiquidity, setMinLiquidity] = useState(0);
  const [selected, setSelected] = useState<Signal | null>(null);
  const [dryRun, setDryRun] = useState(true);

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
    void api
      .health()
      .then((h) => setDryRun(h.dry_run ?? true))
      .catch(() => setDryRun(true));
  }, [load]);

  useEffect(() => {
    if (!auto) return;
    const timer = setInterval(() => void load(), 10_000);
    return () => clearInterval(timer);
  }, [load, auto]);

  const rows = useMemo(() => {
    let out = signals.slice();
    if (onlyVerified) out = out.filter((s) => assessRisk(s.security).level === "low");
    if (excludeRisky) out = out.filter((s) => assessRisk(s.security).level !== "high");
    if (minLiquidity > 0) out = out.filter((s) => (s.liquidity_usd ?? 0) >= minLiquidity);
    out.sort((a, b) => {
      if (sort === "amount") return b.amount_usd - a.amount_usd;
      if (sort === "liquidity") return b.liquidity_usd - a.liquidity_usd;
      return new Date(b.created_at).getTime() - new Date(a.created_at).getTime();
    });
    return out;
  }, [signals, onlyVerified, excludeRisky, minLiquidity, sort]);

  const select = (s: Signal) => setSelected((cur) => (cur?.id === s.id ? null : s));

  return (
    <div className="space-y-4 lg:grid lg:grid-cols-[1fr_22rem] lg:gap-6 lg:space-y-0">
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
          <span className="badge bg-panel text-slate-300">
            {t("trading.activeSignals")} {rows.length}
          </span>
          <button type="button" className="btn" onClick={() => void load()} aria-busy={loading}>
            {t("common.refresh")}
          </button>
        </div>

        <div className="card flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
          <div className="flex gap-1" role="group" aria-label={t("trading.viewCards")}>
            {(["cards", "table"] as const).map((v) => (
              <button
                key={v}
                onClick={() => setView(v)}
                aria-pressed={view === v}
                className={`btn ${view === v ? "border-accent text-white" : ""}`}
              >
                {v === "cards" ? t("trading.viewCards") : t("trading.viewTable")}
              </button>
            ))}
          </div>

          <label className="flex items-center gap-1.5 text-slate-400">
            <input
              type="checkbox"
              checked={onlyVerified}
              onChange={(e) => setOnlyVerified(e.target.checked)}
              className="h-4 w-4 rounded border-slate-600 bg-surface"
            />
            {t("trading.filterVerified")}
          </label>

          <label className="flex items-center gap-1.5 text-slate-400">
            <input
              type="checkbox"
              checked={excludeRisky}
              onChange={(e) => setExcludeRisky(e.target.checked)}
              className="h-4 w-4 rounded border-slate-600 bg-surface"
            />
            {t("trading.filterExcludeRisky")}
          </label>

          <label className="flex items-center gap-1.5 text-slate-400">
            {t("trading.minLiquidity")}
            <input
              inputMode="numeric"
              onChange={(e) => setMinLiquidity(Number(e.target.value) || 0)}
              className="w-24 rounded border border-slate-700 bg-surface px-2 py-1 text-right"
            />
          </label>

          <label className="flex items-center gap-1.5 text-slate-400">
            <select
              value={sort}
              onChange={(e) => setSort(e.target.value as SortKey)}
              className="rounded border border-slate-700 bg-surface px-2 py-1"
              aria-label={t("trading.sortNewest")}
            >
              <option value="newest">{t("trading.sortNewest")}</option>
              <option value="amount">{t("trading.sortAmount")}</option>
              <option value="liquidity">{t("trading.sortLiquidity")}</option>
            </select>
          </label>

          <label className="ml-auto flex items-center gap-1.5 text-slate-400">
            {t("signals.autoRefresh")}
            <input
              type="checkbox"
              checked={auto}
              onChange={(e) => setAuto(e.target.checked)}
              className="h-4 w-4 rounded border-slate-600 bg-surface"
            />
          </label>
        </div>

        {error && (
          <div role="alert" className="card border-danger/40 text-sm text-danger">
            {error}
          </div>
        )}

        {rows.length === 0 ? (
          <div className="card text-sm text-slate-500">{t("trading.emptyHint")}</div>
        ) : view === "cards" ? (
          <ul className="grid gap-3 sm:grid-cols-2">
            {rows.map((s) => (
              <li key={s.id}>
                <button
                  onClick={() => select(s)}
                  className={`card w-full text-left transition hover:border-accent ${
                    selected?.id === s.id ? "border-accent" : ""
                  }`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono text-xs text-slate-300" title={s.token}>
                      {shorten(s.token)}
                    </span>
                    <RiskLight security={s.security} />
                  </div>
                  <div className="mt-2 flex items-end justify-between gap-2">
                    <span className="text-lg font-semibold text-white">
                      {formatCurrency(s.amount_usd)}
                    </span>
                    <StatusBadge status={s.status} />
                  </div>
                  <dl className="mt-2 grid grid-cols-3 gap-1 text-[11px] text-slate-400">
                    <div>
                      <dt>{t("common.price")}</dt>
                      <dd className="tabular-nums text-slate-200">{formatCurrency(s.price_usd)}</dd>
                    </div>
                    <div>
                      <dt>{t("common.liquidity")}</dt>
                      <dd className="tabular-nums text-slate-200">
                        {formatCurrency(s.liquidity_usd)}
                      </dd>
                    </div>
                    <div>
                      <dt>{t("signals.decay")}</dt>
                      <dd className="tabular-nums text-slate-200">{formatPercent(s.decay, 0)}</dd>
                    </div>
                  </dl>
                  <p className="mt-2 text-[11px] text-slate-500">
                    {s.chain} · {formatRelativeTime(s.created_at)}
                  </p>
                </button>
              </li>
            ))}
          </ul>
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
                  <th scope="col">{t("trading.risk")}</th>
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
                {rows.map((s) => (
                  <tr
                    key={s.id}
                    onClick={() => select(s)}
                    className={`cursor-pointer border-t border-slate-800 hover:bg-panel ${
                      selected?.id === s.id ? "bg-panel" : ""
                    }`}
                  >
                    <td
                      className="py-2 text-xs text-slate-400"
                      title={formatDateTime(s.created_at)}
                    >
                      {formatRelativeTime(s.created_at)}
                    </td>
                    <td>{s.chain}</td>
                    <td className="font-mono text-xs" title={s.token}>
                      {shorten(s.token)}
                    </td>
                    <td>
                      <RiskLight security={s.security} />
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

      <aside className="space-y-4">
        {selected ? (
          <QuickTrade
            token={selected.token}
            chain={selected.chain}
            priceUSD={selected.price_usd}
            liquidityUSD={selected.liquidity_usd}
            security={selected.security}
            dryRun={dryRun}
            onClose={() => setSelected(null)}
          />
        ) : (
          <div className="card text-xs text-slate-500">{t("trading.tokenDetail")}</div>
        )}
      </aside>
    </div>
  );
}

/** 地址/哈希缩写（保留首尾，避免卡片换行）。 */
function shorten(value: string) {
  if (!value) return "—";
  return value.length > 14 ? `${value.slice(0, 6)}…${value.slice(-4)}` : value;
}
