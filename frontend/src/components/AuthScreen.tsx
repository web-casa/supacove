import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { ArrowLeft, KeyRound, Terminal } from "lucide-react";
import { api, ApiError } from "../api/client";
import { control, hasErrors, shown } from "../lib/form";
import { errorMessage } from "../lib/format";
import { spotlight } from "../lib/motion";
import { SplashLayout } from "./SplashLayout";
import { Button } from "./ui/Button";
import { Field } from "./ui/Field";
import { InlineMessage } from "./ui/InlineMessage";

const MIN_PASSWORD = 12;

export function AuthScreen() {
  const qc = useQueryClient();
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
        setNotice("This instance already has an admin account — sign in instead.");
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
      token: isBootstrap && token.trim() === "" ? "Paste the token printed by the bootstrap command." : null,
      username: username.trim() === "" ? "Enter a username." : null,
      password:
        password === ""
          ? "Enter a password."
          : isBootstrap && password.length < MIN_PASSWORD
            ? `Use at least ${MIN_PASSWORD} characters (${password.length} so far).`
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
            <h1>Initialize this instance</h1>
            <p className="muted">
              Create the admin account with a one-time token. The first visitor can never claim the
              instance without it.
            </p>
            <div className="callout">
              <Terminal size={14} aria-hidden />
              <div>
                <p>Run on the server to print a token:</p>
                <code>docker exec &lt;container&gt; /app/supabackup bootstrap</code>
                <p className="muted">Valid for 15 minutes, usable once.</p>
              </div>
            </div>
          </>
        ) : (
          <>
            <h1>Sign in</h1>
            <p className="muted">Sign in to your instance.</p>
          </>
        )}

        <form className="form" onSubmit={onSubmit} noValidate>
          {isBootstrap && (
            <Field label="Bootstrap token" htmlFor="token" error={errors.token}>
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
          <Field label={isBootstrap ? "Admin username" : "Username"} htmlFor="username" error={errors.username}>
            <input
              {...control("username", errors.username)}
              value={username}
              autoComplete="username"
              onChange={(e) => setUsername(e.target.value)}
            />
          </Field>
          <Field
            label="Password"
            htmlFor="password"
            error={errors.password}
            hint={isBootstrap ? `Minimum ${MIN_PASSWORD} characters.` : undefined}
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
          {active.isError && !hasErrors(errors) && <InlineMessage>{errorMessage(active.error)}</InlineMessage>}
          <Button type="submit" variant="primary" block loading={active.isPending}>
            {isBootstrap
              ? bootstrap.isPending
                ? "Creating admin…"
                : "Create admin account"
              : login.isPending
                ? "Signing in…"
                : "Sign in"}
          </Button>
        </form>
      </div>
      {isBootstrap ? (
        <Button variant="ghost" size="sm" icon={ArrowLeft} onClick={() => switchTo("login")}>
          Back to sign in
        </Button>
      ) : (
        <Button variant="ghost" size="sm" icon={KeyRound} onClick={() => switchTo("bootstrap")}>
          Initialize a fresh instance with a CLI token
        </Button>
      )}
    </SplashLayout>
  );
}
