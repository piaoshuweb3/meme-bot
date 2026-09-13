// 后端 API 客户端（浏览器侧）。
// 令牌保存在 localStorage；外部 API 使用 X-API-Key（本面板只用 JWT）。

const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "http://localhost:8080";

export type Health = {
  status: string;
  mode: string;
  chains: string[];
  dry_run?: boolean;
  database?: boolean;
  redis?: boolean;
  signer?: boolean;
  alerts?: Record<string, string>;
};

export type Signal = {
  id: string;
  chain: string;
  token: string;
  source: string;
  amount_usd: number;
  price_usd: number;
  liquidity_usd: number;
  status: string;
  decay: number;
  created_at: string;
  expires_at: string;
  trigger_address?: string;
};

export type AddressProfile = {
  address: string;
  chain: string;
  recent_score: number;
  win_rate: number;
  profit_factor: number;
  max_drawdown: number;
  total_trades: number;
  tags?: string[];
  is_blacklisted: boolean;
  last_active: string;
};

export type Position = {
  id: string;
  chain: string;
  token: string;
  token_symbol?: string;
  entry_price_usd: number;
  current_price_usd: number;
  status: string;
  realized_pnl_usd?: number;
  opened_at: string;
};

export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem("memebot_token");
}

export function setToken(token: string) {
  if (typeof window !== "undefined") window.localStorage.setItem("memebot_token", token);
}

export function clearToken() {
  if (typeof window !== "undefined") window.localStorage.removeItem("memebot_token");
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(init?.headers as Record<string, string> | undefined),
  };
  if (token) headers.Authorization = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}${path}`, { ...init, headers, cache: "no-store" });
  const text = await res.text();
  const body = text ? JSON.parse(text) : {};
  if (!res.ok) {
    throw new Error(body?.error ?? `请求失败：${res.status}`);
  }
  return body as T;
}

export const api = {
  base: API_BASE,

  health: () => request<Health>("/healthz"),

  login: async (email: string, password: string) => {
    const body = await request<{ token: string }>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    });
    setToken(body.token);
    return body;
  },

  register: (email: string, password: string, referral_code?: string) =>
    request<{ user: unknown; api_key: string }>("/api/v1/auth/register", {
      method: "POST",
      body: JSON.stringify({ email, password, referral_code }),
    }),

  me: () => request<{ user: unknown }>("/api/v1/me"),

  signals: (limit = 50) => request<{ count: number; signals: Signal[] }>(`/api/v1/signals?limit=${limit}`),

  positions: (chain = "") =>
    request<{ count: number; positions: Position[] }>(
      `/api/v1/positions${chain ? `?chain=${chain}` : ""}`,
    ),

  topAddresses: (chain = "base", limit = 50) =>
    request<{ count: number; addresses: AddressProfile[] }>(
      `/api/v1/admin/addresses/top?chain=${chain}&limit=${limit}`,
    ),

  plans: () => request<{ plans: unknown[] }>("/api/v1/plans"),

  pause: (reason: string, minutes = 60) =>
    request<{ status: string }>("/api/v1/system/pause", {
      method: "POST",
      body: JSON.stringify({ reason, duration_minutes: minutes }),
    }),

  resume: () => request<{ status: string }>("/api/v1/system/resume", { method: "POST" }),
};
