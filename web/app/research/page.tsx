"use client";

import { useCallback, useEffect, useMemo, useState } from "react";

import { api, type OutcomeCoverage, type OutcomeStat } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

const WINDOWS = [24, 168, 720]; // 24 小时 / 7 天 / 30 天

/**
 * 研究对照页：影子表现跟踪的可视化。
 *
 * 页面要传达的核心不是"收益数字"，而是**证据强度**：
 *   - 无样本 → 显示 —（不是 0，缺失 ≠ 零收益）；
 *   - 样本未达门槛 → 明确标注"样本不足，不构成结论"，不允许读者据此调参。
 * 这是能不能证伪过滤器的前提，因此界面上必须与数字一样显眼。
 */
export default function ResearchPage() {
  const { t, formatPercent } = useI18n();
  const [hours, setHours] = useState(24);
  const [data, setData] = useState<OutcomeCoverage | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setError("");
      setData(await api.outcomeCoverage(hours));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setData(null);
    } finally {
      setLoading(false);
    }
  }, [hours]);

  useEffect(() => {
    void load();
  }, [load]);

  const grouped = useMemo(() => {
    const map = new Map<string, OutcomeStat[]>();
    for (const st of data?.stats ?? []) {
      const list = map.get(st.decision) ?? [];
      list.push(st);
      map.set(st.decision, list);
    }
    const order = ["FILTERED", "SIGNALED", "EXECUTED"];
    return order.filter((d) => map.has(d)).map((d) => ({ decision: d, stats: map.get(d) ?? [] }));
  }, [data]);

  const threshold = data?.min_samples_for_calibration ?? 0;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-semibold text-white">{t("research.title")}</h1>
        <span className="badge bg-panel text-slate-300">
          {t("research.records")} {data?.records ?? 0}
        </span>
        {threshold > 0 && (
          <span className="badge bg-panel text-slate-300">
            {t("research.threshold")} {threshold}
          </span>
        )}
        <button type="button" className="btn" onClick={() => void load()} aria-busy={loading}>
          {t("common.refresh")}
        </button>
        <div className="ml-auto flex gap-1" role="group" aria-label={t("research.hours")}>
          {WINDOWS.map((h) => (
            <button
              key={h}
              onClick={() => setHours(h)}
              aria-pressed={hours === h}
              className={`btn ${hours === h ? "border-accent text-white" : ""}`}
            >
              {h}
              {t("research.hoursShort")}
            </button>
          ))}
        </div>
      </div>

      <div className="card space-y-2 text-xs text-slate-400">
        <p className="text-slate-300">{t("research.subtitle")}</p>
        <p>{t("research.explain")}</p>
        <p className="text-amber-300/80">{t("research.semantics")}</p>
      </div>

      {error && (
        <div role="alert" className="card border-danger/40 text-sm text-danger">
          {error}
        </div>
      )}

      {grouped.length === 0 ? (
        <div className="card text-sm text-slate-500">{t("research.empty")}</div>
      ) : (
        grouped.map(({ decision, stats }) => (
          <section key={decision} className="space-y-2">
            <h2 className="text-sm font-semibold text-slate-200">
              {decisionLabel(decision, t)}
            </h2>
            <div className="card overflow-x-auto">
              <table className="w-full text-sm">
                <caption className="sr-only">
                  {t("research.title")} · {decisionLabel(decision, t)}
                </caption>
                <thead className="text-left text-slate-400">
                  <tr>
                    <th scope="col" className="py-2">
                      {t("research.horizon")}
                    </th>
                    <th scope="col" className="text-right">
                      {t("research.eligible")}
                    </th>
                    <th scope="col" className="text-right">
                      {t("research.completed")}
                    </th>
                    <th scope="col" className="text-right">
                      {t("research.missing")}
                    </th>
                    <th scope="col" className="text-right">
                      {t("research.median")}
                    </th>
                    <th scope="col" className="text-right">
                      {t("research.positiveRate")}
                    </th>
                    <th scope="col">{t("research.calibratable")}</th>
                  </tr>
                </thead>
                <tbody>
                  {stats.map((st) => (
                    <tr key={`${st.decision}-${st.horizon}`} className="border-t border-slate-800">
                      <td className="py-2 font-mono text-xs">{st.horizon}</td>
                      <td className="text-right tabular-nums">{st.eligible}</td>
                      <td className="text-right tabular-nums">{st.completed}</td>
                      <td
                        className={`text-right tabular-nums ${
                          st.missing > 0 ? "text-amber-300" : "text-slate-400"
                        }`}
                      >
                        {st.missing}
                      </td>
                      <td
                        className={`text-right tabular-nums ${
                          st.median === null
                            ? "text-slate-500"
                            : st.median >= 0
                              ? "text-ok"
                              : "text-danger"
                        }`}
                      >
                        {st.median === null ? "—" : formatPercent(st.median)}
                      </td>
                      <td className="text-right tabular-nums text-slate-200">
                        {st.positive_rate === null ? "—" : formatPercent(st.positive_rate)}
                      </td>
                      <td>
                        {st.calibratable ? (
                          <span className="text-emerald-300">{t("research.calibratable")}</span>
                        ) : (
                          <span className="text-amber-300">{t("research.insufficient")}</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        ))
      )}
    </div>
  );
}

/** 决策分层本地化（未知值原样显示，便于后端扩展时降级）。 */
function decisionLabel(decision: string, t: (key: string) => string): string {
  switch (decision) {
    case "FILTERED":
      return t("research.fFiltered");
    case "SIGNALED":
      return t("research.fSignaled");
    case "EXECUTED":
      return t("research.fExecuted");
    default:
      return decision;
  }
}
