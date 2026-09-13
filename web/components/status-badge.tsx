"use client";

import { useI18n } from "@/lib/i18n";

/** 信号状态徽章：状态码 → 本地化文案 + 语义化配色。 */
export function StatusBadge({ status }: { status: string }) {
  const { t } = useI18n();

  const styles: Record<string, string> = {
    pending: "bg-warn/20 text-warn",
    confirmed: "bg-ok/20 text-ok",
    executed: "bg-accent/20 text-accent",
    expired: "bg-slate-700 text-slate-300",
    rejected: "bg-danger/20 text-danger",
  };

  const labels: Record<string, string> = {
    pending: t("signalsStatus.pending"),
    confirmed: t("signalsStatus.confirmed"),
    executed: t("signalsStatus.executed"),
    expired: t("signalsStatus.expired"),
    rejected: t("signalsStatus.rejected"),
  };

  const label = labels[status] ?? status;

  return (
    <span className={`badge ${styles[status] ?? "bg-slate-700 text-slate-300"}`} title={status}>
      {label}
    </span>
  );
}
