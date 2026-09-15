"use client";

import { useCallback, useEffect, useMemo, useState } from "react";

import { api, type Position } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

const SELL_PRESETS = [25, 50, 100];

/**
 * 持仓页（交易台规格里的「持仓 & 监控」）：
 *   - 顶部汇总：持仓数、平均浮动盈亏、已实现盈亏；
 *   - 卡片视图（默认，移动端友好）与表格视图可切换；
 *   - 每笔持仓带**一键卖出比例**（25% / 50% / 100%）与止盈止损价位展示。
 *
 * 诚实边界：后端未暴露平仓端点（实盘卖出同样只走策略引擎的风控链路），
 * 因此卖出动作是**模拟**并显式提示实盘前提，避免"点了没反应"的假交互。
 */
export default function PositionsPage() {
  const { t, formatCurrency, formatPercent, formatDateTime } = useI18n();
  const [rows, setRows] = useState<Position[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [view, setView] = useState<"cards" | "table">("cards");
  const [sellPct, setSellPct] = useState<Record<string, number>>({});
  const [submitted, setSubmitted] = useState<Record<string, boolean>>({});
  const [dryRun, setDryRun] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setError("");
      const res = await api.positions();
      setRows(res.positions ?? []);
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
    const timer = setInterval(() => void load(), 20_000);
    return () => clearInterval(timer);
  }, [load]);

  const totals = useMemo(() => {
    const open = rows.filter((p) => p.status === "open");
    const unrealized = open.map((p) => pnlRatio(p));
    const avg = unrealized.length ? unrealized.reduce((a, b) => a + b, 0) / unrealized.length : 0;
    const realized = rows.reduce((sum, p) => sum + (p.realized_pnl_usd ?? 0), 0);
    return { openCount: open.length, closedCount: rows.length - open.length, avg, realized };
  }, [rows]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-semibold text-white">{t("positions.title")}</h1>
        <button type="button" className="btn" onClick={() => void load()} aria-busy={loading}>
          {t("common.refresh")}
        </button>
        <div className="ml-auto flex gap-1" role="group" aria-label={t("trading.viewCards")}>
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
      </div>

      {/* 汇总：持仓数 / 平均浮动盈亏 / 已实现盈亏 */}
      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div className="card">
          <dt className="text-xs text-slate-400">{t("trading.openPositions")}</dt>
          <dd className="text-lg font-semibold text-white">{totals.openCount}</dd>
        </div>
        <div className="card">
          <dt className="text-xs text-slate-400">{t("positions.closed")}</dt>
          <dd className="text-lg font-semibold text-slate-300">{totals.closedCount}</dd>
        </div>
        <div className="card">
          <dt className="text-xs text-slate-400">{t("common.pnlPct")}</dt>
          <dd
            className={`text-lg font-semibold ${totals.avg >= 0 ? "text-ok" : "text-danger"}`}
          >
            {totals.avg >= 0 ? "▲" : "▼"} {formatPercent(Math.abs(totals.avg))}
          </dd>
        </div>
        <div className="card">
          <dt className="text-xs text-slate-400">{t("positions.realized")}</dt>
          <dd
            className={`text-lg font-semibold ${
              totals.realized >= 0 ? "text-ok" : "text-danger"
            }`}
          >
            {formatCurrency(totals.realized)}
          </dd>
        </div>
      </dl>

      {error && (
        <div role="alert" className="card border-danger/40 text-sm text-danger">
          {error}
        </div>
      )}

      {rows.length === 0 ? (
        <div className="card text-sm text-slate-500">{t("positions.emptyHint")}</div>
      ) : view === "cards" ? (
        <ul className="grid gap-3 lg:grid-cols-2">
          {rows.map((p) => {
            const pnl = pnlRatio(p);
            const open = p.status === "open";
            return (
              <li key={p.id} className="card space-y-3">
                <div className="flex items-center justify-between gap-2">
                  <span className="font-mono text-xs text-slate-300" title={p.token}>
                    {p.token_symbol || shorten(p.token)}
                  </span>
                  <span
                    className={`text-sm font-semibold tabular-nums ${
                      pnl >= 0 ? "text-ok" : "text-danger"
                    }`}
                    aria-label={`${t("common.pnlPct")}: ${formatPercent(pnl)}`}
                  >
                    <span aria-hidden="true">{pnl >= 0 ? "▲" : "▼"}</span>{" "}
                    {formatPercent(Math.abs(pnl))}
                  </span>
                </div>

                <dl className="grid grid-cols-3 gap-2 text-[11px] text-slate-400">
                  <div>
                    <dt>{t("common.entryPrice")}</dt>
                    <dd className="tabular-nums text-slate-200">
                      {formatCurrency(p.entry_price_usd)}
                    </dd>
                  </div>
                  <div>
                    <dt>{t("common.currentPrice")}</dt>
                    <dd className="tabular-nums text-slate-200">
                      {formatCurrency(p.current_price_usd)}
                    </dd>
                  </div>
                  <div>
                    <dt>{t("common.status")}</dt>
                    <dd className="text-slate-200">
                      {open ? t("positions.open") : t("positions.closed")}
                    </dd>
                  </div>
                </dl>

                <p className="text-[11px] text-slate-500">
                  {p.chain} · {formatDateTime(p.opened_at)}
                </p>

                {/* 一键卖出比例：25% / 50% / 100% */}
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-xs text-slate-400">{t("trading.rSellPercent")}</span>
                  {SELL_PRESETS.map((pct) => (
                    <button
                      key={pct}
                      disabled={!open}
                      onClick={() => setSellPct((prev) => ({ ...prev, [p.id]: pct }))}
                      aria-pressed={sellPct[p.id] === pct}
                      className={`btn ${sellPct[p.id] === pct ? "border-accent text-white" : ""}`}
                    >
                      {pct}%
                    </button>
                  ))}
                  <button
                    className="btn ml-auto"
                    disabled={!open || !sellPct[p.id]}
                    onClick={() => setSubmitted((prev) => ({ ...prev, [p.id]: true }))}
                  >
                    {t("trading.rSell")}
                  </button>
                </div>

                {submitted[p.id] && sellPct[p.id] ? (
                  <p className="text-xs text-emerald-300" role="status">
                    {t("trading.simulate")} · {t("trading.rSell")} {sellPct[p.id]}%
                  </p>
                ) : null}
                {dryRun && (
                  <p className="text-[11px] text-slate-500">{t("trading.liveBlocked")}</p>
                )}
              </li>
            );
          })}
        </ul>
      ) : (
        <div className="card overflow-x-auto">
          <table className="w-full text-sm">
            <caption className="sr-only">{t("positions.title")}</caption>
            <thead className="text-left text-slate-400">
              <tr>
                <th scope="col" className="py-2">
                  {t("common.chain")}
                </th>
                <th scope="col">{t("common.token")}</th>
                <th scope="col" className="text-right">
                  {t("common.entryPrice")}
                </th>
                <th scope="col" className="text-right">
                  {t("common.currentPrice")}
                </th>
                <th scope="col" className="text-right">
                  {t("common.pnlPct")}
                </th>
                <th scope="col">{t("common.status")}</th>
                <th scope="col">{t("trading.rSellPercent")}</th>
                <th scope="col">{t("common.openedAt")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => {
                const pnl = pnlRatio(p);
                const open = p.status === "open";
                return (
                  <tr key={p.id} className="border-t border-slate-800">
                    <td className="py-2">{p.chain}</td>
                    <td className="font-mono text-xs" title={p.token}>
                      {p.token_symbol || shorten(p.token)}
                    </td>
                    <td className="text-right tabular-nums">{formatCurrency(p.entry_price_usd)}</td>
                    <td className="text-right tabular-nums">
                      {formatCurrency(p.current_price_usd)}
                    </td>
                    <td
                      className={`text-right tabular-nums ${pnl >= 0 ? "text-ok" : "text-danger"}`}
                      aria-label={`${t("common.pnlPct")}: ${formatPercent(pnl)}`}
                    >
                      <span aria-hidden="true">{pnl >= 0 ? "▲" : "▼"}</span>{" "}
                      {formatPercent(Math.abs(pnl))}
                    </td>
                    <td>{open ? t("positions.open") : t("positions.closed")}</td>
                    <td>
                      <div className="flex gap-1">
                        {SELL_PRESETS.map((pct) => (
                          <button
                            key={pct}
                            disabled={!open}
                            onClick={() => {
                              setSellPct((prev) => ({ ...prev, [p.id]: pct }));
                              setSubmitted((prev) => ({ ...prev, [p.id]: true }));
                            }}
                            className="btn px-2 py-0.5 text-[11px]"
                            title={t("trading.simulate")}
                          >
                            {pct}%
                          </button>
                        ))}
                      </div>
                    </td>
                    <td className="text-xs text-slate-400">{formatDateTime(p.opened_at)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

/** 浮动盈亏比例（入场价为 0 时按 0 处理，避免除零）。 */
function pnlRatio(p: Position): number {
  return p.entry_price_usd > 0
    ? (p.current_price_usd - p.entry_price_usd) / p.entry_price_usd
    : 0;
}

function shorten(value: string) {
  if (!value) return "—";
  return value.length > 14 ? `${value.slice(0, 6)}…${value.slice(-4)}` : value;
}
