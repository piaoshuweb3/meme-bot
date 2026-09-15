"use client";

import { useMemo, useState } from "react";

import { RiskLight, assessRisk } from "@/components/risk-light";
import type { SecuritySummary } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

const PRESETS = [0.1, 0.5, 1, 5];
const SLIPPAGE_PRESETS = [10, 50, 100];

/**
 * 快捷交易面板（交易台的「一键操作 + 风险默认可见 + 状态反馈」）。
 *
 * 重要边界：后端**没有**暴露下单端点——实盘执行只走策略引擎
 * （信号 → 风控 → 私有通道 → 签名器）。因此这里提供的是**模拟预览**
 * （含滑点后的预估到手数量）并明确说明实盘前提，不做"看似可下单但实际无效"的假交互。
 */
export function QuickTrade({
  token,
  chain,
  priceUSD,
  liquidityUSD,
  security,
  dryRun,
  onClose,
}: {
  token: string;
  chain: string;
  priceUSD?: number;
  liquidityUSD?: number;
  security?: SecuritySummary;
  dryRun: boolean;
  onClose?: () => void;
}) {
  const { t, formatCurrency, formatNumber } = useI18n();
  const [side, setSide] = useState<"buy" | "sell">("buy");
  const [amountUSD, setAmountUSD] = useState(0.5);
  const [custom, setCustom] = useState("");
  const [slippageBps, setSlippageBps] = useState(50);
  const [submitted, setSubmitted] = useState(false);

  const { level, reasons } = assessRisk(security);
  const price = priceUSD ?? 0;

  const preview = useMemo(() => {
    const effective = amountUSD * (1 - slippageBps / 10_000);
    const estTokens = price > 0 ? effective / price : 0;
    const impact = liquidityUSD && liquidityUSD > 0 ? amountUSD / liquidityUSD : 0;
    return { effective, estTokens, impact };
  }, [amountUSD, slippageBps, price, liquidityUSD]);

  // 高风险标的：连模拟入口都显式拦截，避免"按钮存在即暗示可买"的误操作
  const blocked = level === "high";

  return (
    <section className="card space-y-4" aria-label={t("trading.buy")}>
      <header className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-white">
            {t("trading.buy")} / {t("trading.sell")}
          </h2>
          <p className="text-xs text-slate-500">
            {chain} · {token.slice(0, 10)}…
          </p>
        </div>
        <RiskLight security={security} />
      </header>

      <div className="flex gap-2">
        {(["buy", "sell"] as const).map((s) => (
          <button
            key={s}
            aria-pressed={side === s}
            onClick={() => setSide(s)}
            className={`btn flex-1 ${side === s ? "border-accent text-white" : ""}`}
          >
            {s === "buy" ? t("trading.buy") : t("trading.sell")}
          </button>
        ))}
      </div>

      <div className="space-y-2">
        <span className="text-xs text-slate-400">{t("trading.amount")}</span>
        <div className="flex flex-wrap gap-2">
          {PRESETS.map((v) => (
            <button
              key={v}
              onClick={() => {
                setAmountUSD(v);
                setCustom("");
              }}
              className={`btn ${amountUSD === v && !custom ? "border-accent text-white" : ""}`}
            >
              {v}
            </button>
          ))}
          <input
            inputMode="decimal"
            value={custom}
            onChange={(e) => {
              setCustom(e.target.value);
              const n = Number(e.target.value);
              if (!Number.isNaN(n) && n > 0) setAmountUSD(n);
            }}
            placeholder={t("trading.custom")}
            className="btn w-24 text-right"
            aria-label={t("trading.custom")}
          />
        </div>
      </div>

      <div className="space-y-2">
        <span className="text-xs text-slate-400">{t("trading.slippage")}</span>
        <div className="flex gap-2">
          {SLIPPAGE_PRESETS.map((bps) => (
            <button
              key={bps}
              onClick={() => setSlippageBps(bps)}
              className={`btn ${slippageBps === bps ? "border-accent text-white" : ""}`}
            >
              {(bps / 100).toFixed(1)}%
            </button>
          ))}
        </div>
      </div>

      <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
        <dt className="text-slate-400">{t("trading.estimatedReceive")}</dt>
        <dd className="text-right text-slate-200">{formatNumber(preview.estTokens)}</dd>
        <dt className="text-slate-400">{t("trading.amount")}</dt>
        <dd className="text-right text-slate-200">{formatCurrency(preview.effective)}</dd>
        <dt className="text-slate-400">{t("common.liquidity")}</dt>
        <dd className="text-right text-slate-200">{formatCurrency(liquidityUSD ?? 0)}</dd>
        <dt className="text-slate-400">{t("trading.riskSummary")}</dt>
        <dd className="text-right">
          {reasons.length ? (
            <span className="text-amber-300">{reasons.map((r) => t(`trading.${r}`)).join(" · ")}</span>
          ) : (
            <span className="text-emerald-300">{t("trading.riskSafe")}</span>
          )}
        </dd>
      </dl>

      <p className="rounded border border-slate-800 bg-surface px-3 py-2 text-xs text-slate-400">
        {dryRun ? t("trading.liveBlocked") : t("trading.simulate")}
      </p>

      <div className="flex gap-2">
        <button
          className="btn flex-1"
          disabled={blocked}
          onClick={() => setSubmitted(true)}
          title={blocked ? t("trading.riskHigh") : undefined}
        >
          {t("trading.simulate")}
        </button>
        {onClose && (
          <button className="btn" onClick={onClose}>
            {t("trading.cancel")}
          </button>
        )}
      </div>

      {submitted && (
        <p className="text-xs text-emerald-300" role="status">
          {t("trading.simulate")} · {side === "buy" ? t("trading.buy") : t("trading.sell")}{" "}
          {formatCurrency(preview.effective)}
        </p>
      )}
      {blocked && reasons.length > 0 && (
        <p className="text-xs text-rose-300" role="alert">
          {t("trading.riskHigh")}: {reasons.map((r) => t(`trading.${r}`)).join(" · ")}
        </p>
      )}
    </section>
  );
}
