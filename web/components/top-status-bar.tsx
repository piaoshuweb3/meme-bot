"use client";

import { useEffect, useMemo, useState } from "react";

import { useRealtime } from "@/hooks/use-realtime";
import { api, type Health, type Position } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

/**
 * 交易台顶部状态栏：实时连接状态、运行模式（dry_run/live）、监控链、
 * 持仓数、今日盈亏与 UTC 时钟。
 *
 * 断线时状态点变红并脉冲提示——对应「状态反馈必须显式可见」的要求。
 */
export function TopStatusBar() {
  const { t, formatCurrency } = useI18n();
  const { connected } = useRealtime(() => {});
  const [health, setHealth] = useState<Health | null>(null);
  const [positions, setPositions] = useState<Position[]>([]);
  const [nowUTC, setNowUTC] = useState("");

  useEffect(() => {
    void api.health().then(setHealth).catch(() => setHealth(null));
    void api.positions().then((r) => setPositions(r.positions ?? [])).catch(() => setPositions([]));
    const tick = () => setNowUTC(new Date().toISOString().slice(11, 19));
    tick();
    const id = setInterval(tick, 1_000);
    return () => clearInterval(id);
  }, []);

  const pnl = useMemo(
    () => positions.reduce((sum, p) => sum + (p.realized_pnl_usd ?? 0), 0),
    [positions],
  );
  const openCount = positions.filter((p) => p.status === "open").length;
  const dryRun = health?.dry_run ?? true;

  return (
    <div className="border-b border-slate-800 bg-panel/60">
      <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-5 gap-y-1 px-6 py-2 text-xs">
        <span className="inline-flex items-center gap-1.5">
          <span
            className={`h-2 w-2 rounded-full ${
              connected ? "bg-emerald-400" : "animate-pulse bg-rose-500"
            }`}
            aria-hidden
          />
          <span className={connected ? "text-emerald-300" : "text-rose-300"}>
            {connected ? t("trading.connected") : t("trading.offline")}
          </span>
        </span>

        <span className="text-slate-400">
          {t("trading.mode")}:{" "}
          <span className={dryRun ? "text-amber-300" : "text-rose-300"}>{health?.mode ?? "—"}</span>
        </span>

        <span className="text-slate-400">
          {t("common.chain")}:{" "}
          <span className="text-slate-200">{(health?.chains ?? []).join(" / ") || "—"}</span>
        </span>

        <span className="text-slate-400">
          {t("trading.openPositions")}: <span className="text-slate-200">{openCount}</span>
        </span>

        <span className="text-slate-400">
          {t("trading.todayPnl")}:{" "}
          <span className={pnl >= 0 ? "text-emerald-300" : "text-rose-300"}>
            {formatCurrency(pnl)}
          </span>
        </span>

        <span className="ml-auto tabular-nums text-slate-500">
          {nowUTC} {t("trading.utc")}
        </span>
      </div>
    </div>
  );
}
