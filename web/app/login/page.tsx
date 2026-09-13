"use client";

import { useRouter } from "next/navigation";
import { useId, useState } from "react";

import { api, clearToken } from "@/lib/api";
import { useI18n } from "@/lib/i18n";

/**
 * 登录 / 注册页。
 *
 * 无障碍与国际化要点：
 *   - 表单控件均有真实 <label>（htmlFor + id）与正确的 autocomplete/type；
 *   - 结果消息使用 aria-live 播报，错误用 role="alert"；
 *   - 所有文案来自字典，密码规则提示本地化。
 */
export default function LoginPage() {
  const { t } = useI18n();
  const router = useRouter();
  const emailId = useId();
  const passwordId = useId();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [message, setMessage] = useState("");
  const [isError, setIsError] = useState(false);
  const [apiKey, setApiKey] = useState("");
  const [busy, setBusy] = useState(false);

  const onLogin = async () => {
    setBusy(true);
    setMessage("");
    setIsError(false);
    try {
      await api.login(email, password);
      setMessage(t("login.loginSuccess"));
      router.push("/");
    } catch (e) {
      setIsError(true);
      setMessage(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const onRegister = async () => {
    setBusy(true);
    setMessage("");
    setIsError(false);
    setApiKey("");
    try {
      const res = await api.register(email, password);
      setApiKey(res.api_key);
      setMessage(t("login.registerSuccess"));
    } catch (e) {
      setIsError(true);
      setMessage(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mx-auto max-w-md space-y-4">
      <h1 className="text-2xl font-semibold text-white">{t("login.title")}</h1>

      <form
        className="card space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          void onLogin();
        }}
      >
        <div className="space-y-1">
          <label htmlFor={emailId} className="block text-xs text-slate-400">
            {t("login.email")}
          </label>
          <input
            id={emailId}
            type="email"
            inputMode="email"
            autoComplete="email"
            required
            placeholder={t("login.emailPlaceholder")}
            className="w-full rounded-lg border border-slate-700 bg-surface px-3 py-2 text-sm
                       focus:border-accent focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </div>

        <div className="space-y-1">
          <label htmlFor={passwordId} className="block text-xs text-slate-400">
            {t("login.password")}
          </label>
          <input
            id={passwordId}
            type="password"
            autoComplete="current-password"
            minLength={8}
            required
            placeholder={t("login.passwordPlaceholder")}
            className="w-full rounded-lg border border-slate-700 bg-surface px-3 py-2 text-sm
                       focus:border-accent focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>

        <div className="flex flex-wrap gap-2">
          <button type="submit" className="btn" disabled={busy} aria-busy={busy}>
            {t("login.signIn")}
          </button>
          <button
            type="button"
            className="btn"
            disabled={busy}
            aria-busy={busy}
            onClick={() => void onRegister()}
          >
            {t("login.signUp")}
          </button>
          <button
            type="button"
            className="btn ml-auto"
            onClick={() => {
              clearToken();
              setIsError(false);
              setMessage(t("login.tokenCleared"));
            }}
          >
            {t("login.signOut")}
          </button>
        </div>

        {message && (
          <p
            role={isError ? "alert" : "status"}
            aria-live="polite"
            className={`text-sm ${isError ? "text-danger" : "text-slate-300"}`}
          >
            {message}
          </p>
        )}

        {apiKey && (
          <output
            className="block break-all rounded-lg border border-warn/40 bg-warn/10 p-2 font-mono text-xs text-warn"
            aria-label={t("login.apiKeyWarning")}
          >
            {apiKey}
          </output>
        )}
      </form>

      <p className="text-xs text-slate-500">{t("login.passwordHint")}</p>
    </div>
  );
}
