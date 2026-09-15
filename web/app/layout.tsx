import type { Metadata } from "next";

import { BottomNav } from "@/components/bottom-nav";
import { SiteFooter } from "@/components/site-footer";
import { SiteHeader } from "@/components/site-header";
import { TopStatusBar } from "@/components/top-status-bar";
import { I18nProvider } from "@/lib/i18n";

import "./globals.css";

/**
 * 根布局（Server Component）。
 *
 * 国际化说明：`<html lang>` 由 I18nProvider 在客户端按用户 locale 同步
 * （服务端首屏固定使用默认 locale，避免 hydration 内容不一致）。
 */
export const metadata: Metadata = {
  title: "meme-bot · Smart Money Dashboard | 聪明钱监控面板",
  description:
    "Low-frequency, high-precision micro-cap monitoring, smart-money copy trading, risk control and alerting. 低频高精度迷你币监控 · 聪明钱跟单 · 风控与告警。",
  applicationName: "meme-bot",
  robots: { index: false, follow: false }, // 内部面板：默认不被搜索引擎索引
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN" dir="ltr">
      <body>
        <I18nProvider>
          <a
            href="#main"
            className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-50
                       focus:rounded focus:bg-panel focus:px-3 focus:py-2 focus:text-sm focus:text-white"
          >
            Skip to main content
          </a>
          <SiteHeader />
          <TopStatusBar />
          {/* pb-20：为移动端底部导航留出空间，避免内容被遮挡 */}
          <main id="main" className="mx-auto max-w-6xl px-6 py-8 pb-20 md:pb-8">
            {children}
          </main>
          <SiteFooter />
          <BottomNav />
        </I18nProvider>
      </body>
    </html>
  );
}
