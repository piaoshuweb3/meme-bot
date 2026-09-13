"use client";

import { useCallback, useEffect, useState } from "react";

import { api, type Position } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

/** 持仓页面：浮动盈亏按 locale 格式化，并带可访问的盈亏语义描述。 */
export default function PositionsPage() {
  const { t, formatCurrency, formatPercent, formatDateTime } = useI18n();
  const [rows, setRows] = useState<Position[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

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
    const timer = setInterval(() => void load(), 20_000);
    return () => clearInterval(timer);
  }, [load]);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <h1 className="text-2xl font-semibold text-white">{t("positions.title")}</h1>
        <button type="button" className="btn" onClick={() => void load()} aria-busy={loading}>
          {t("common.refresh")}
        </button>
      </div>

      {error && (
        <div role="alert" className="card border-danger/40 text-sm text-danger">
          {error}
        </div>
      )}

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
              <th scope="col">{t("common.openedAt")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr>
                <td colSpan={7} className="py-4 text-center text-slate-500">
                  {t("positions.emptyHint")}
                </td>
              </tr>
            )}
            {rows.map((p) => {
              const pnl =
                p.entry_price_usd > 0
                  ? (p.current_price_usd - p.entry_price_usd) / p.entry_price_usd
                  : 0;
              return (
                <tr key={p.id} className="border-t border-slate-800">
                  <td className="py-2">{p.chain}</td>
                  <td className="font-mono text-xs" title={p.token}>
                    {p.token_symbol || shorten(p.token)}
                  </td>
                  <td className="text-right tabular-nums">{formatCurrency(p.entry_price_usd)}</td>
                  <td className="text-right tabular-nums">{formatCurrency(p.current_price_usd)}</td>
                  <td
                    className={`text-right tabular-nums ${pnl >= 0 ? "text-ok" : "text-danger"}`}
                    aria-label={`${t("common.pnlPct")}: ${formatPercent(pnl)}`}
                  >
                    <span aria-hidden="true">{pnl >= 0 ? "▲" : "▼"}</span>{" "}
                    {formatPercent(Math.abs(pnl))}
                  </td>
                  <td>{p.status === "open" ? t("positions.open") : t("positions.closed")}</td>
                  <td className="text-xs text-slate-400">{formatDateTime(p.opened_at)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function shorten(value: string) {
  if (!value) return "—";
  return value.length > 14 ? `${value.slice(0, 6)}…${value.slice(-4)}` : value;
}
