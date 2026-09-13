"use client";

import { useId } from "react";

import { LOCALES, useI18n, type LocaleCode } from "@/lib/i18n";

/**
 * 语言切换器。
 *
 * 无障碍（a11y）要点：
 *   - 原生 <select>：键盘可达、屏幕阅读器友好，且不额外增加依赖；
 *   - label 与控件通过 id/for 关联，并带 aria-label 兜底；
 *   - 使用 aria-live 播报切换结果。
 */
export function LanguageSwitcher() {
  const { locale, setLocale, t } = useI18n();
  const selectId = useId();

  return (
    <div className="flex items-center gap-2">
      <label htmlFor={selectId} className="sr-only">
        {t("common.language")}
      </label>
      <span aria-hidden="true" className="text-slate-500">
        🌐
      </span>
      <select
        id={selectId}
        value={locale}
        aria-label={t("common.switchLanguage")}
        onChange={(e) => setLocale(e.target.value as LocaleCode)}
        className="rounded-lg border border-slate-700 bg-panel px-2 py-1 text-xs text-slate-200
                   focus:border-accent focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
      >
        {LOCALES.map((l) => (
          <option key={l.code} value={l.code}>
            {l.label}
          </option>
        ))}
      </select>
      <span aria-live="polite" className="sr-only">
        {t("common.language")}: {LOCALES.find((l) => l.code === locale)?.label}
      </span>
    </div>
  );
}
