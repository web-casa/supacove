import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { ArrowLeft, KeyRound, Terminal } from "lucide-react";
import { api, ApiError } from "../api/client";
import { useI18n } from "../i18n";
import { control, hasErrors, shown } from "../lib/form";
import { errorMessage } from "../lib/format";
import { spotlight } from "../lib/motion";
import { SplashLayout } from "./SplashLayout";
import { Button } from "./ui/Button";
import { Field } from "./ui/Field";
import { InlineMessage } from "./ui/InlineMessage";
import { LangSwitch } from "./ui/LangSwitch";

const MIN_PASSWORD = 12;

export function AuthScreen() {
  const qc = useQueryClient();
  const { t } = useI18n();
  const [mode, setMode] = useState<"login" | "bootstrap">("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [token, setToken] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  const invalidate = () => {
    qc.setQueryData(["me"], undefined);
    qc.invalidateQueries({ queryKey: ["me"] });
  };

  const login = useMutation({
    mutationFn: () => api.login(username, password),
    onSuccess: invalidate,
  });
  const bootstrap = useMutation({
    mutationFn: () => api.bootstrap(token, username, password),
    onSuccess: invalidate,
    // 409 means an admin already exists: fall back to the login form.
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) {
        switchTo("login");
        setNotice(t("auth.notice.adminExists"));
      }
    },
  });

  function switchTo(next: "login" | "bootstrap") {
    setMode(next);
    setSubmitted(false);
    setNotice(null);
    login.reset();
    if (next === "bootstrap") bootstrap.reset();
  }

  const isBootstrap = mode === "bootstrap";
  const errors = shown(
    {
      token: isBootstrap && token.trim() === "" ? t("auth.error.token") : null,
      username: username.trim() === "" ? t("auth.error.username") : null,
      password:
        password === ""
          ? t("auth.error.password")
          : isBootstrap && password.length < MIN_PASSWORD
            ? t("auth.error.passwordShort", {
                n: MIN_PASSWORD,
                len: password.length,
              })
            : null,
    },
    submitted,
  );

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitted(true);
    setNotice(null);
    const blocking =
      username.trim() === "" ||
      password === "" ||
      (isBootstrap && (token.trim() === "" || password.length < MIN_PASSWORD));
    if (blocking) return;
    if (isBootstrap) bootstrap.mutate();
    else login.mutate();
  }

  const active = isBootstrap ? bootstrap : login;

  return (
    <SplashLayout>
      <div className="auth-card spotlight" key={mode} onPointerMove={spotlight}>
        {isBootstrap ? (
          <>
            <h1>{t("auth.bootstrap.title")}</h1>
            <p className="muted">{t("auth.bootstrap.sub")}</p>
            <div className="callout">
              <Terminal size={14} aria-hidden />
              <div>
                <p>{t("auth.bootstrap.runOnServer")}</p>
                <code>
                  docker exec &lt;container&gt; /app/supacove bootstrap
                </code>
                <p className="muted">{t("auth.bootstrap.validFor")}</p>
              </div>
            </div>
          </>
        ) : (
          <>
            <h1>{t("auth.signIn.title")}</h1>
            <p className="muted">{t("auth.signIn.sub")}</p>
          </>
        )}

        <form className="form" onSubmit={onSubmit} noValidate>
          {isBootstrap && (
            <Field
              label={t("auth.field.token")}
              htmlFor="token"
              error={errors.token}
            >
              <input
                {...control("token", errors.token)}
                className="mono"
                value={token}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setToken(e.target.value)}
              />
            </Field>
          )}
          <Field
            label={
              isBootstrap
                ? t("auth.field.adminUsername")
                : t("auth.field.username")
            }
            htmlFor="username"
            error={errors.username}
          >
            <input
              {...control("username", errors.username)}
              value={username}
              autoComplete="username"
              onChange={(e) => setUsername(e.target.value)}
            />
          </Field>
          <Field
            label={t("auth.field.password")}
            htmlFor="password"
            error={errors.password}
            hint={
              isBootstrap
                ? t("auth.field.passwordHint", { n: MIN_PASSWORD })
                : undefined
            }
          >
            <input
              {...control("password", errors.password)}
              type="password"
              value={password}
              autoComplete={isBootstrap ? "new-password" : "current-password"}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          {notice && <InlineMessage tone="info">{notice}</InlineMessage>}
          {active.isError && !hasErrors(errors) && (
            <InlineMessage>{errorMessage(active.error)}</InlineMessage>
          )}
          <Button
            type="submit"
            variant="primary"
            block
            loading={active.isPending}
          >
            {isBootstrap
              ? bootstrap.isPending
                ? t("auth.action.creatingAdmin")
                : t("auth.action.createAdmin")
              : login.isPending
                ? t("auth.action.signingIn")
                : t("auth.action.signIn")}
          </Button>
        </form>
      </div>
      <div className="splash-foot">
        {isBootstrap ? (
          <Button
            variant="ghost"
            size="sm"
            icon={ArrowLeft}
            onClick={() => switchTo("login")}
          >
            {t("auth.action.backToSignIn")}
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="sm"
            icon={KeyRound}
            onClick={() => switchTo("bootstrap")}
          >
            {t("auth.action.bootstrapEntry")}
          </Button>
        )}
        <LangSwitch />
      </div>
    </SplashLayout>
  );
}
