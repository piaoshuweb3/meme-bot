"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { useI18n } from "@/lib/i18n";

/**
 * 移动端底部导航（桌面端隐藏）：发现 / 交易 / 持仓 / 我的。
 *
 * 中间「交易」做成上浮的大按钮——对应移动端「关键动作做成悬浮大按钮」的要求。
 */
export function BottomNav() {
  const { t } = useI18n();
  const pathname = usePathname();

  const items = [
    { href: "/", label: t("nav.overview"), icon: "◎" },
    { href: "/signals", label: t("mobileNav.discover"), icon: "⇅" },
    { href: "/positions", label: t("mobileNav.positionsShort"), icon: "▤" },
    { href: "/research", label: t("research.title"), icon: "◱" },
    { href: "/addresses", label: t("mobileNav.profile"), icon: "◍" },
  ];

  const isActive = (href: string) =>
    href === "/" ? pathname === "/" : pathname.startsWith(href);

  return (
    <nav
      className="fixed inset-x-0 bottom-0 z-20 border-t border-slate-800 bg-surface/95 backdrop-blur md:hidden"
      aria-label={t("mobileNav.discover")}
    >
      <ul className="mx-auto flex max-w-6xl items-stretch justify-around">
        {items.map((item, idx) => (
          <li key={item.href} className="flex-1">
            <Link
              href={item.href}
              aria-current={isActive(item.href) ? "page" : undefined}
              className={`flex flex-col items-center gap-0.5 px-2 py-2 text-[11px] ${
                isActive(item.href) ? "text-white" : "text-slate-400"
              } ${idx === 1 ? "-mt-3 rounded-t-lg bg-panel pt-3 shadow-lg" : ""}`}
            >
              <span aria-hidden className="text-base leading-none">
                {item.icon}
              </span>
              {item.label}
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  );
}
