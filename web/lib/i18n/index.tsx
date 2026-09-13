"use client";

/**
 * 轻量国际化（i18n）实现，遵循常见标准：
 *   - locale 使用 BCP 47 标签（zh-CN / en-US），并映射到 `Intl.*` 使用的区域标识；
 *   - 文案全部来自类型安全字典（编译期保证 key 一致），组件内零硬编码；
 *   - 日期/数字/货币/百分比一律通过 `Intl` 按 locale 格式化，不做字符串拼接；
 *   - 切换语言时同步更新 <html lang> 与 dir 属性（无障碍与 SEO 需要）；
 *   - 语言偏好持久化到 localStorage，首次访问按 navigator.language 自动判定。
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { enUS } from "./locales/en-US";
import { zhCN, type Messages } from "./locales/zh-CN";

/** 支持的 locale（BCP 47）。新增语言只需在此登记 + 提供字典。 */
export const LOCALES = [
  { code: "zh-CN", label: "简体中文", htmlLang: "zh-CN", intlLocale: "zh-CN" },
  { code: "en-US", label: "English", htmlLang: "en", intlLocale: "en-US" },
] as const;

export type LocaleCode = (typeof LOCALES)[number]["code"];

/** 默认 locale（SSR 首屏使用，避免 hydration 不一致）。 */
export const DEFAULT_LOCALE: LocaleCode = "zh-CN";

const DICTS: Record<LocaleCode, Messages> = {
  "zh-CN": zhCN,
  "en-US": enUS,
};

const STORAGE_KEY = "memebot.locale";

function isLocaleCode(value: unknown): value is LocaleCode {
  return LOCALES.some((l) => l.code === value);
}

/** 解析初始 locale：已保存偏好 → 浏览器语言 → 默认。 */
export function resolveInitialLocale(): LocaleCode {
  if (typeof window === "undefined") return DEFAULT_LOCALE;
  const saved = window.localStorage.getItem(STORAGE_KEY);
  if (isLocaleCode(saved)) return saved;
  const nav = (window.navigator.languages?.[0] ?? window.navigator.language ?? "").toLowerCase();
  if (nav.startsWith("zh")) return "zh-CN";
  if (nav.startsWith("en")) return "en-US";
  return DEFAULT_LOCALE;
}

export function localeMeta(code: LocaleCode) {
  return LOCALES.find((l) => l.code === code) ?? LOCALES[0];
}

type Vars = Record<string, string | number>;

/** 取 "a.b.c" 路径上的文案；缺失时返回 key 本身，便于发现遗漏。 */
function lookup(dict: Messages, key: string): string {
  const parts = key.split(".");
  let node: unknown = dict;
  for (const p of parts) {
    if (typeof node !== "object" || node === null) return key;
    node = (node as Record<string, unknown>)[p];
  }
  return typeof node === "string" ? node : key;
}

function interpolate(template: string, vars?: Vars): string {
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in vars ? String(vars[name]) : match,
  );
}

export type I18nValue = {
  locale: LocaleCode;
  setLocale: (code: LocaleCode) => void;
  t: (key: string, vars?: Vars) => string;
  formatNumber: (value: number, options?: Intl.NumberFormatOptions) => string;
  formatCurrency: (value: number, currency?: string) => string;
  formatCompact: (value: number) => string;
  formatPercent: (ratio: number, fractionDigits?: number) => string;
  formatDateTime: (value: string | number | Date) => string;
  formatRelativeTime: (value: string | number | Date) => string;
};

const I18nContext = createContext<I18nValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<LocaleCode>(DEFAULT_LOCALE);

  // 首屏之后同步真实偏好（避免 SSR/CSR 内容不一致）
  useEffect(() => {
    const resolved = resolveInitialLocale();
    setLocaleState(resolved);
  }, []);

  // 同步 <html lang> / dir，并持久化偏好
  useEffect(() => {
    const meta = localeMeta(locale);
    if (typeof document !== "undefined") {
      document.documentElement.lang = meta.htmlLang;
      document.documentElement.dir = "ltr";
    }
    if (typeof window !== "undefined") {
      window.localStorage.setItem(STORAGE_KEY, locale);
    }
  }, [locale]);

  const setLocale = useCallback((code: LocaleCode) => {
    if (isLocaleCode(code)) setLocaleState(code);
  }, []);

  const value = useMemo<I18nValue>(() => {
    const dict = DICTS[locale];
    const intl = localeMeta(locale).intlLocale;

    const numberFormat = (options?: Intl.NumberFormatOptions) =>
      new Intl.NumberFormat(intl, options);

    return {
      locale,
      setLocale,
      t: (key, vars) => interpolate(lookup(dict, key), vars),
      formatNumber: (v, options) => numberFormat(options).format(v),
      formatCurrency: (v, currency = "USD") =>
        new Intl.NumberFormat(intl, {
          style: "currency",
          currency,
          maximumFractionDigits: v >= 1000 ? 0 : 2,
        }).format(v),
      formatCompact: (v) =>
        new Intl.NumberFormat(intl, {
          notation: "compact",
          maximumFractionDigits: 1,
        }).format(v),
      formatPercent: (ratio, fractionDigits = 2) =>
        new Intl.NumberFormat(intl, {
          style: "percent",
          minimumFractionDigits: fractionDigits,
          maximumFractionDigits: fractionDigits,
        }).format(ratio),
      formatDateTime: (v) =>
        new Intl.DateTimeFormat(intl, {
          dateStyle: "medium",
          timeStyle: "medium",
        }).format(new Date(v)),
      formatRelativeTime: (v) => {
        const target = new Date(v).getTime();
        const diffSeconds = Math.round((target - Date.now()) / 1000);
        const rtf = new Intl.RelativeTimeFormat(intl, { numeric: "auto" });
        const thresholds: Array<[Intl.RelativeTimeFormatUnit, number]> = [
          ["second", 60],
          ["minute", 60],
          ["hour", 24],
          ["day", 7],
          ["week", 4.34524],
          ["month", 12],
        ];
        let value = diffSeconds;
        for (const [unit, limit] of thresholds) {
          if (Math.abs(value) < limit) return rtf.format(Math.round(value), unit);
          value /= limit;
        }
        return rtf.format(Math.round(value), "year");
      },
    };
  }, [locale, setLocale]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

/** 读取 i18n 上下文（必须在 I18nProvider 内使用）。 */
export function useI18n(): I18nValue {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useI18n must be used inside <I18nProvider>");
  return ctx;
}
