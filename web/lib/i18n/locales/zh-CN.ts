/**
 * 简体中文文案（基准语言）。
 *
 * 约定：
 *   - 所有 key 使用「命名空间.语义」结构，禁止在组件中硬编码任何面向用户的字符串；
 *   - 占位符使用 {name} 形式，由 t(key, vars) 插值；
 *   - 本文件是文案类型基准，其它语言必须实现同一结构（编译期强校验）。
 */
export const zhCN = {
  trading: {
    terminalTitle: "交易台",
    connected: "实时已连接",
    offline: "实时未连接",
    mode: "模式",
    utc: "UTC",
    todayPnl: "今日盈亏",
    openPositions: "持仓",
    activeSignals: "活跃信号",
    risk: "风险",
    riskLow: "低风险",
    riskMedium: "中风险",
    riskHigh: "高风险",
    riskUnknown: "未知",
    buy: "买入",
    sell: "卖出",
    amount: "金额",
    slippage: "滑点",
    estimatedReceive: "预估到手",
    simulate: "模拟预览",
    liveBlocked: "当前为 dry_run：实盘下单需开启 live 模式并配置签名器",
    confirm: "确认",
    cancel: "取消",
    riskSummary: "风险摘要",
    riskHoneypot: "蜜罐合约",
    riskMintable: "存在增发权限",
    riskBlacklist: "存在黑名单权限",
    riskNotOpenSource: "合约未开源",
    riskHighTax: "买卖税率偏高",
    riskOwnershipNotRenounced: "权限未放弃",
    riskSafe: "未发现已知风险特征",
    rSellPercent: "卖出比例",
    rSell: "卖出",
    custom: "自定义",
    filterVerified: "只看已验证",
    filterExcludeRisky: "排除高风险",
    viewCards: "卡片",
    viewTable: "表格",
    sortNewest: "最新",
    sortAmount: "金额",
    sortLiquidity: "流动性",
    minLiquidity: "最低流动性（USD）",
    emptyHint: "暂无信号，等待监控命中…",
    tokenDetail: "代币详情",
    graph: "图表",
    history: "历史",
    noHistory: "暂无历史记录",
  },
  mobileNav: {
    discover: "发现",
    trading: "交易",
    positionsShort: "持仓",
    profile: "我的",
  },
  common: {
    appName: "meme-bot",
    refresh: "刷新",
    retry: "重试",
    loading: "加载中…",
    empty: "暂无数据",
    unknown: "未知",
    copy: "复制",
    copied: "已复制",
    close: "关闭",
    skipToContent: "跳到主要内容",
    language: "语言",
    switchLanguage: "切换语言",
    yes: "是",
    no: "否",
    error: "错误",
    warning: "警告",
    critical: "严重",
    info: "提示",
    chain: "链",
    token: "代币",
    address: "地址",
    amount: "金额",
    amountUsd: "金额（USD）",
    price: "价格",
    liquidity: "流动性",
    volume24h: "24h 成交量",
    status: "状态",
    time: "时间",
    actions: "操作",
    score: "评分",
    winRate: "胜率",
    profitFactor: "盈亏比",
    maxDrawdown: "最大回撤",
    trades: "交易数",
    tags: "标签",
    entryPrice: "入场价",
    currentPrice: "现价",
    pnlPct: "浮动盈亏",
    openedAt: "开仓时间",
  },

  nav: {
    label: "主导航",
    overview: "总览",
    signals: "信号流",
    addresses: "地址榜",
    positions: "持仓",
    login: "登录",
  },

  layout: {
    title: "meme-bot · 聪明钱监控面板",
    description: "低频高精度迷你币监控 · 聪明钱跟单 · 风控与告警",
    dryRunBanner: "默认 dry_run：不会发送真实交易",
    disclaimer:
      "本工具仅供技术研究与学习使用，不构成任何投资建议；加密资产风险极高，请自行承担全部风险。",
  },

  health: {
    sectionTitle: "系统状态",
    mode: "运行模式",
    modeDryRun: "dry_run（不下真实单）",
    modeLive: "live（已开启真实交易）",
    chains: "已就绪链",
    database: "数据库",
    databaseOk: "已连接",
    databaseDown: "未连接",
    databaseOkHint: "SaaS 功能可用",
    databaseDownHint: "降级模式：用户/订阅接口返回 503",
    signer: "签名器",
    signerOk: "已加载",
    signerEmpty: "未加载",
    signerOkHint: "可签名（live 前请二次确认）",
    signerEmptyHint: "dry_run 下无需签名器",
    redis: "Redis",
    smartMoney: "聪明钱数据源",
    connected: "已连接",
    notConnected: "未连接",
    configured: "已配置",
    notConfigured: "未配置",
    alertChannels: "告警通道",
    unavailable: "无法连接后端（{base}）：{error}",
  },

  risk: {
    title: "风险控制",
    description:
      "熔断后执行层会在每单前校验，所有交易（含 Agent 发起）都会被拒绝，直到手动恢复。",
    pause: "一键熔断（60 分钟）",
    resume: "恢复交易",
    pauseReason: "Web 面板人工熔断",
    paused: "已熔断",
    resumed: "交易已恢复",
    state: "当前状态",
    running: "运行中",
    pausedWithReason: "已熔断：{reason}",
    activeSignals: "活跃信号 {count} 个",
  },

  signals: {
    title: "信号流",
    autoRefresh: "自动刷新（10s）",
    emptyHint:
      "暂无活跃信号。信号只在满足「安全过滤 + 流动性门槛 + 大额买入 + 冷却窗口」后才会产生。",
    staleHint: "列表可能已过期，点击刷新获取最新数据。",
    source: "来源",
    triggerAddress: "触发地址",
    decay: "衰减",
    sourceFollow: "聪明钱跟单",
    sourceAutonomous: "自主漏斗",
    sourceAgent: "Agent",
  },

  signalsStatus: {
    pending: "待确认",
    confirmed: "已确认",
    executed: "已执行",
    expired: "已过期",
    rejected: "已拒绝",
  },

  addresses: {
    title: "高分地址榜",
    adminOnly: "需要管理员令牌（admin / super_admin）",
    emptyHint: "暂无画像数据（worker 会定时重算：make worker）",
    filterChain: "筛选链",
  },

  positions: {
    realized: "已实现盈亏",
    title: "持仓",
    emptyHint: "当前无持仓",
    open: "持有中",
    closed: "已平仓",
  },

  login: {
    title: "登录 / 注册",
    email: "邮箱",
    emailPlaceholder: "you@example.com",
    password: "密码",
    passwordPlaceholder: "至少 8 位",
    signIn: "登录",
    signUp: "注册",
    signOut: "清除令牌",
    loginSuccess: "登录成功，令牌已保存到本地。",
    registerSuccess:
      "注册成功。API Key 仅显示一次，请立即保存（切勿提交到代码仓库）。",
    tokenCleared: "已清除本地令牌。",
    passwordHint:
      "密码使用 bcrypt 哈希存储；访问令牌为 HS256 JWT，密钥来自后端环境变量 MEMEBOT_AUTH_JWT_SECRET。",
    apiKeyWarning: "API Key（仅显示一次）",
  },

  realtime: {
    live: "实时",
    polling: "轮询（降级）",
    label: "数据通道",
  },

  time: {
    justNow: "刚刚",
    minutesAgo: "{n} 分钟前",
    hoursAgo: "{n} 小时前",
    daysAgo: "{n} 天前",
  },
} as const;

/**
 * 把字面量类型放宽为 `string`，但**保留完整的 key 结构**。
 *
 * 这样其它语言（en-US 等）只需实现同一结构即可，不必逐条复制中文字面量类型，
 * 同时在编译期仍能发现「缺失 key / 多余 key」这类本应避免的问题。
 */
type DeepStringRecord<T> = {
  [K in keyof T]: T[K] extends string ? string : DeepStringRecord<T[K]>;
};

export type Messages = DeepStringRecord<typeof zhCN>;
export type MessageKey = string;
