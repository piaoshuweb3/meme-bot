"use client";

/**
 * WebSocket 实时推送客户端。
 *
 * 设计要点：
 *   - 指数退避 + 抖动自动重连（避免断网恢复时同时冲击服务端）；
 *   - 连接状态回调驱动 UI（徽章显示"实时/降级"）；
 *   - 解析失败静默丢弃（不因单条脏数据打断连接）。
 */
export type WsEventType = "signal" | "order" | "position" | "alert" | "system";

export type WsEvent = {
  type: WsEventType;
  at: string;
  data?: unknown;
};

export type WsStatus = "connecting" | "open" | "closed";

export type RealtimeOptions = {
  onEvent: (evt: WsEvent) => void;
  onStatus?: (status: WsStatus) => void;
  path?: string;
  maxBackoffMs?: number;
};

/** 由 REST 基址推导 WS 地址（http→ws / https→wss）。 */
export function wsBaseUrl(): string {
  const api = process.env.NEXT_PUBLIC_API_BASE ?? "http://localhost:8080";
  return api.replace(/^http/, "ws").replace(/\/$/, "");
}

/** 建立实时连接，返回关闭函数。 */
export function connectRealtime(opts: RealtimeOptions): () => void {
  let socket: WebSocket | null = null;
  let closed = false;
  let attempt = 0;
  const maxBackoff = opts.maxBackoffMs ?? 15000;

  const schedule = () => {
    if (closed) return;
    attempt += 1;
    const base = Math.min(500 * 2 ** (attempt - 1), maxBackoff);
    const jitter = Math.random() * 300;
    window.setTimeout(open, base + jitter);
  };

  const open = () => {
    if (closed) return;
    opts.onStatus?.("connecting");
    const url = `${wsBaseUrl()}${opts.path ?? "/ws"}`;
    try {
      socket = new WebSocket(url);
    } catch {
      schedule();
      return;
    }

    socket.onopen = () => {
      attempt = 0;
      opts.onStatus?.("open");
    };
    socket.onmessage = (event) => {
      try {
        opts.onEvent(JSON.parse(String(event.data)) as WsEvent);
      } catch {
        /* 忽略非法载荷 */
      }
    };
    socket.onclose = () => {
      opts.onStatus?.("closed");
      schedule();
    };
    socket.onerror = () => {
      try {
        socket?.close();
      } catch {
        /* ignore */
      }
    };
  };

  open();
  return () => {
    closed = true;
    try {
      socket?.close();
    } catch {
      /* ignore */
    }
  };
}
