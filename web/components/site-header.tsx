"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { LanguageSwitcher } from "@/components/language-switcher";
import { useI18n } from "@/lib/i18n";

/** 顶部导航（客户端组件：需要当前 locale 与激活态高亮）。 */
export function SiteHeader() {
  const { t } = useI18n();
  const pathname = usePathname();

  const items = [
    { href: "/", label: t("nav.overview") },
    { href: "/signals", label: t("nav.signals") },
    { href: "/addresses", label: t("nav.addresses") },
    { href: "/positions", label: t("nav.positions") },
    { href: "/research", label: t("research.title") },
    { href: "/login", label: t("nav.login") },
  ];

  const isActive = (href: string) =>
    href === "/" ? pathname === "/" : pathname.startsWith(href);

  return (
    <header className="sticky top-0 z-10 border-b border-slate-800 bg-surface/95 backdrop-blur">
      <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-6 gap-y-2 px-6 py-4">
        <Link href="/" className="text-lg font-semibold text-white" aria-label={t("common.appName")}>
          {t("common.appName")}
        </Link>

        <nav aria-label={t("nav.label")}>
          <ul className="flex flex-wrap gap-4 text-sm">
            {items.map((item) => (
              <li key={item.href}>
                <Link
                  href={item.href}
                  aria-current={isActive(item.href) ? "page" : undefined}
                  className={
                    isActive(item.href)
                      ? "text-white underline decoration-accent decoration-2 underline-offset-4"
                      : "text-slate-400 hover:text-white focus:text-white"
                  }
                >
                  {item.label}
                </Link>
              </li>
            ))}
          </ul>
        </nav>

        <div className="ml-auto flex items-center gap-4">
          <span className="hidden text-xs text-slate-500 md:inline">{t("layout.dryRunBanner")}</span>
          <LanguageSwitcher />
        </div>
      </div>
    </header>
  );
}
