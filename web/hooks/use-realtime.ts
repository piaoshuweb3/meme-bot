"use client";

import { useEffect, useRef, useState } from "react";

import { connectRealtime, type WsEvent, type WsStatus } from "@/lib/ws";

/**
 * 订阅服务端实时事件。
 *
 * 用法：
 *   const { status, connected } = useRealtime((evt) => { ... });
 *
 * 说明：`connected=false` 时页面应继续使用轮询（本 hook 不会自动停轮询），
 * 这样断线期间数据仍然可更新（降级而非失效）。
 */
export function useRealtime(onEvent: (evt: WsEvent) => void) {
  const [status, setStatus] = useState<WsStatus>("connecting");
  const handlerRef = useRef(onEvent);
  handlerRef.current = onEvent;

  useEffect(() => {
    const close = connectRealtime({
      onStatus: setStatus,
      onEvent: (evt) => handlerRef.current(evt),
    });
    return close;
  }, []);

  return { status, connected: status === "open" };
}
