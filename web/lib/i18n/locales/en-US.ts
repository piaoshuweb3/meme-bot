/**
 * English (US) messages.
 *
 * This file must implement exactly the same structure as `zh-CN.ts`
 * (enforced at compile time via the `Messages` type), which guarantees
 * that no key is silently missing in any locale.
 */
import type { Messages } from "./zh-CN";

export const enUS: Messages = {
  common: {
    appName: "meme-bot",
    refresh: "Refresh",
    retry: "Retry",
    loading: "Loading…",
    empty: "No data",
    unknown: "Unknown",
    copy: "Copy",
    copied: "Copied",
    close: "Close",
    skipToContent: "Skip to main content",
    language: "Language",
    switchLanguage: "Switch language",
    yes: "Yes",
    no: "No",
    error: "Error",
    warning: "Warning",
    critical: "Critical",
    info: "Info",
    chain: "Chain",
    token: "Token",
    address: "Address",
    amount: "Amount",
    amountUsd: "Amount (USD)",
    price: "Price",
    liquidity: "Liquidity",
    volume24h: "Volume (24h)",
    status: "Status",
    time: "Time",
    actions: "Actions",
    score: "Score",
    winRate: "Win rate",
    profitFactor: "Profit factor",
    maxDrawdown: "Max drawdown",
    trades: "Trades",
    tags: "Tags",
    entryPrice: "Entry price",
    currentPrice: "Mark price",
    pnlPct: "Unrealized PnL",
    openedAt: "Opened at",
  },

  nav: {
    label: "Primary navigation",
    overview: "Overview",
    signals: "Signals",
    addresses: "Addresses",
    positions: "Positions",
    login: "Sign in",
  },

  layout: {
    title: "meme-bot · Smart Money Dashboard",
    description: "Low-frequency, high-precision micro-cap monitoring · copy trading · risk & alerts",
    dryRunBanner: "dry_run by default: no real trades are sent",
    disclaimer:
      "This tool is for technical research and educational purposes only and is not investment advice. Crypto assets are extremely risky; you bear all risk.",
  },

  health: {
    sectionTitle: "System status",
    mode: "Mode",
    modeDryRun: "dry_run (no real orders)",
    modeLive: "live (real trading enabled)",
    chains: "Ready chains",
    database: "Database",
    databaseOk: "Connected",
    databaseDown: "Not connected",
    databaseOkHint: "SaaS features available",
    databaseDownHint: "Degraded: user/subscription endpoints return 503",
    signer: "Signer",
    signerOk: "Loaded",
    signerEmpty: "Not loaded",
    signerOkHint: "Signing enabled (double-check before going live)",
    signerEmptyHint: "Not required in dry_run",
    redis: "Redis",
    smartMoney: "Smart-money source",
    connected: "Connected",
    notConnected: "Not connected",
    configured: "Configured",
    notConfigured: "Not configured",
    alertChannels: "Alert channels",
    unavailable: "Cannot reach backend ({base}): {error}",
  },

  risk: {
    title: "Risk control",
    description:
      "While paused, every order (including agent-initiated ones) is rejected until you resume manually.",
    pause: "Pause (60 min)",
    resume: "Resume trading",
    pauseReason: "Manual pause from web dashboard",
    paused: "Paused",
    resumed: "Trading resumed",
    state: "State",
    running: "Running",
    pausedWithReason: "Paused: {reason}",
    activeSignals: "{count} active signal(s)",
  },

  signals: {
    title: "Signal stream",
    autoRefresh: "Auto refresh (10s)",
    emptyHint:
      "No active signals yet. A signal is only produced after passing the security filter, liquidity floor, large-buy check and cooldown window.",
    staleHint: "Data may be stale — hit refresh to reload.",
    source: "Source",
    triggerAddress: "Trigger address",
    decay: "Decay",
    sourceFollow: "Smart-money copy",
    sourceAutonomous: "Autonomous funnel",
    sourceAgent: "Agent",
  },

  signalsStatus: {
    pending: "Pending",
    confirmed: "Confirmed",
    executed: "Executed",
    expired: "Expired",
    rejected: "Rejected",
  },

  addresses: {
    title: "Top-scored addresses",
    adminOnly: "Requires an admin token (admin / super_admin)",
    emptyHint: "No profiles yet (run `make worker` to rebuild periodically)",
    filterChain: "Filter by chain",
  },

  positions: {
    title: "Positions",
    emptyHint: "No open positions",
    open: "Open",
    closed: "Closed",
  },

  login: {
    title: "Sign in / Sign up",
    email: "Email",
    emailPlaceholder: "you@example.com",
    password: "Password",
    passwordPlaceholder: "At least 8 characters",
    signIn: "Sign in",
    signUp: "Sign up",
    signOut: "Clear token",
    loginSuccess: "Signed in. The token is stored locally.",
    registerSuccess:
      "Account created. The API key is shown only once — store it now and never commit it.",
    tokenCleared: "Local token cleared.",
    passwordHint:
      "Passwords are stored as bcrypt hashes; access tokens are HS256 JWTs signed with the backend secret MEMEBOT_AUTH_JWT_SECRET.",
    apiKeyWarning: "API key (shown once)",
  },

  realtime: {
    live: "Live",
    polling: "Polling (fallback)",
    label: "Data channel",
  },

  time: {
    justNow: "just now",
    minutesAgo: "{n} min ago",
    hoursAgo: "{n} h ago",
    daysAgo: "{n} d ago",
  },
};
