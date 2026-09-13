"use client";

import { useI18n } from "@/lib/i18n";

/** 页脚：固定展示风险免责声明（合规要求）。 */
export function SiteFooter() {
  const { t } = useI18n();
  return (
    <footer className="mx-auto max-w-6xl px-6 py-10 text-xs text-slate-500">
      <p>{t("layout.disclaimer")}</p>
    </footer>
  );
}
