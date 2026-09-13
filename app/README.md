# meme-bot 移动端（Flutter）

一套代码输出 iOS / Android，对接 Go 后端 REST API。

## 运行

```bash
cd app
flutter pub get

# Android 模拟器（10.0.2.2 指向宿主机）
flutter run --dart-define=API_BASE=http://10.0.2.2:8080

# iOS 模拟器 / 桌面
flutter run --dart-define=API_BASE=http://127.0.0.1:8080

# 真机：换成局域网 IP
flutter run --dart-define=API_BASE=http://192.168.x.x:8080
```

## 目录

```
app/lib/
├── main.dart                          # 入口 + 底部导航
├── core/api_client.dart               # REST 客户端（JWT 内存保存）
└── features/
    ├── dashboard/dashboard_page.dart  # 后端状态 + 一键熔断/恢复
    ├── signals/signals_page.dart      # 信号流
    └── positions/positions_page.dart  # 持仓与浮动盈亏
```

## 已实现 / 待实现

| 功能 | 状态 |
|------|------|
| 总览、信号、持仓 | ✅ 已完成 |
| 一键熔断 / 恢复 | ✅ 已完成 |
| 后端地址热切换 | ✅ 已完成 |
| WebSocket 实时推送 | ⏳ Stage 6 |
| 本地推送通知（Critical 告警） | ⏳ Stage 6 |
| 生物识别二次确认（平仓/改风控参数） | ⏳ Stage 6 |
| 令牌安全存储（flutter_secure_storage） | ⏳ Stage 6 |

## 安全须知

- App **不接触任何私钥**，所有交易由后端签名器（环境变量/KMS）完成；
- 高风险操作（平仓、修改风控参数）最终版本必须经过生物识别二次确认；
- 请勿把后端地址与令牌写入代码仓库。
