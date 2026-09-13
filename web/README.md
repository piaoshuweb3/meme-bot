# meme-bot Web Dashboard

Next.js 15（App Router）+ TypeScript + Tailwind 前端，对接 Go 后端 REST API。

## 快速开始

```bash
cd web
npm install
# 后端默认地址 http://localhost:8080，可用环境变量覆盖：
#   NEXT_PUBLIC_API_BASE=http://127.0.0.1:8080
npm run dev          # http://localhost:3000
```

构建检查：

```bash
npm run typecheck
npm run build
```

## 页面

| 路径 | 说明 |
|------|------|
| `/` | 总览：运行模式、已就绪链、数据库/签名器状态、一键熔断/恢复、最新信号 |
| `/signals` | 实时信号流（10s 自动刷新） |
| `/addresses` | 高分地址榜（需要 admin / super_admin 令牌） |
| `/positions` | 当前持仓与浮动盈亏 |
| `/login` | 登录 / 注册 / 轮换 API Key 提示 |

## 说明

- 令牌保存在 `localStorage`，由 `lib/api.ts` 统一注入 `Authorization: Bearer`。
- 后端未启用（数据库不可用）时，SaaS 接口返回 `503`，页面会显示降级提示，不影响链上监控视图。
- 本面板默认指向 `dry_run` 后端；请勿在未完成风控校验前切换到 `live`。
