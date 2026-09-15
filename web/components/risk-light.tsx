"use client";

import type { SecuritySummary } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

export type RiskLevel = "low" | "medium" | "high" | "unknown";

/**
 * 依据合约安全摘要判定风险等级（绿 / 黄 / 红）。
 *
 * 设计原则：任何买入动作前风险必须**默认可见**（不能一键无提示），
 * 因此把判定逻辑独立出来，供列表、详情与交易面板共用同一口径。
 */
export function assessRisk(sec?: SecuritySummary): { level: RiskLevel; reasons: string[] } {
  if (!sec) return { level: "unknown", reasons: [] };
  const order: Record<RiskLevel, number> = { unknown: 0, low: 1, medium: 2, high: 3 };
  let level: RiskLevel = "low";
  const reasons: string[] = [];
  const bump = (l: RiskLevel) => {
    if (order[l] > order[level]) level = l;
  };

  if (sec.is_honeypot) {
    reasons.push("riskHoneypot");
    bump("high");
  }
  if (sec.has_mint) {
    reasons.push("riskMintable");
    bump("high");
  }
  if (sec.has_blacklist) {
    reasons.push("riskBlacklist");
    bump("high");
  }
  if (sec.is_open_source === false) {
    reasons.push("riskNotOpenSource");
    bump("medium");
  }
  if (sec.ownership_renounced === false) {
    reasons.push("riskOwnershipNotRenounced");
    bump("medium");
  }
  if ((sec.buy_tax ?? 0) > 0.1 || (sec.sell_tax ?? 0) > 0.1) {
    reasons.push("riskHighTax");
    bump("medium");
  }
  // 后端综合判定为 risky 但未命中上述明细时，至少给到黄色预警
  if (sec.risky && level === "low") bump("medium");
  return { level, reasons };
}

const DOT: Record<RiskLevel, string> = {
  low: "bg-emerald-400",
  medium: "bg-amber-400",
  high: "bg-rose-500",
  unknown: "bg-slate-500",
};

const TEXT: Record<RiskLevel, string> = {
  low: "text-emerald-300",
  medium: "text-amber-300",
  high: "text-rose-300",
  unknown: "text-slate-400",
};

const LABEL: Record<RiskLevel, string> = {
  low: "riskLow",
  medium: "riskMedium",
  high: "riskHigh",
  unknown: "riskUnknown",
};

/** 风险指示灯：颜色 + 文案 + 悬浮显示具体风险项。 */
export function RiskLight({
  security,
  showLabel = true,
}: {
  security?: SecuritySummary;
  showLabel?: boolean;
}) {
  const { t } = useI18n();
  const { level, reasons } = assessRisk(security);
  const title = reasons.length
    ? reasons.map((r) => t(`trading.${r}`)).join(" · ")
    : t("trading.riskSafe");

  return (
    <span
      className="inline-flex items-center gap-1.5"
      title={title}
      aria-label={`${t("trading.risk")}: ${t(`trading.${LABEL[level]}`)}`}
    >
      <span className={`inline-block h-2.5 w-2.5 shrink-0 rounded-full ${DOT[level]}`} aria-hidden />
      {showLabel && <span className={`text-xs ${TEXT[level]}`}>{t(`trading.${LABEL[level]}`)}</span>}
    </span>
  );
}
