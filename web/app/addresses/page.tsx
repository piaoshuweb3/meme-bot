"use client";

import { useCallback, useEffect, useState } from "react";

import { api, type AddressProfile } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

const CHAINS = ["base", "solana", "bsc"];

/** 高分地址榜：评分/胜率/回撤等指标按 locale 格式化。 */
export default function AddressesPage() {
  const { t, formatNumber, formatPercent } = useI18n();
  const [chain, setChain] = useState("base");
  const [rows, setRows] = useState<AddressProfile[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setError("");
      const res = await api.topAddresses(chain, 50);
      setRows(res.addresses ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [chain]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-semibold text-white">{t("addresses.title")}</h1>
        <label className="flex items-center gap-2 text-sm text-slate-400">
          <span className="sr-only sm:not-sr-only">{t("addresses.filterChain")}</span>
          <select
            aria-label={t("addresses.filterChain")}
            className="rounded-lg border border-slate-700 bg-panel px-3 py-1.5 text-sm
                       focus:border-accent focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
            value={chain}
            onChange={(e) => setChain(e.target.value)}
          >
            {CHAINS.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        </label>
        <button type="button" className="btn" onClick={() => void load()} aria-busy={loading}>
          {t("common.refresh")}
        </button>
        <span className="ml-auto text-xs text-slate-500">{t("addresses.adminOnly")}</span>
      </div>

      {error && (
        <div role="alert" className="card border-danger/40 text-sm text-danger">
          {error}
        </div>
      )}

      <div className="card overflow-x-auto">
        <table className="w-full text-sm">
          <caption className="sr-only">{t("addresses.title")}</caption>
          <thead className="text-left text-slate-400">
            <tr>
              <th scope="col" className="py-2">
                {t("common.address")}
              </th>
              <th scope="col" className="text-right">
                {t("common.score")}
              </th>
              <th scope="col" className="text-right">
                {t("common.winRate")}
              </th>
              <th scope="col" className="text-right">
                {t("common.profitFactor")}
              </th>
              <th scope="col" className="text-right">
                {t("common.maxDrawdown")}
              </th>
              <th scope="col" className="text-right">
                {t("common.trades")}
              </th>
              <th scope="col">{t("common.tags")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr>
                <td colSpan={7} className="py-4 text-center text-slate-500">
                  {t("addresses.emptyHint")}
                </td>
              </tr>
            )}
            {rows.map((a) => (
              <tr key={`${a.chain}-${a.address}`} className="border-t border-slate-800">
                <td className="py-2 font-mono text-xs" title={a.address}>
                  {shorten(a.address)}
                </td>
                <td className="text-right font-semibold tabular-nums text-white">
                  {formatNumber(a.recent_score, { maximumFractionDigits: 3 })}
                </td>
                <td className="text-right tabular-nums">{formatPercent(a.win_rate, 1)}</td>
                <td className="text-right tabular-nums">
                  {formatNumber(a.profit_factor, { maximumFractionDigits: 2 })}
                </td>
                <td className="text-right tabular-nums">{formatPercent(a.max_drawdown, 1)}</td>
                <td className="text-right tabular-nums">{formatNumber(a.total_trades)}</td>
                <td className="text-xs text-slate-400">{a.tags?.join(", ") || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function shorten(value: string) {
  if (!value) return "—";
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value;
}
