import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError, type User } from "./api/client";

// Flow: unauthenticated → bootstrap form if the instance is fresh (requires a
// one-time token from the local CLI), otherwise login. Authenticated → stub
// dashboard (real screens start in Phase 2).

type Gate = "checking" | "login" | "bootstrap";

export default function App() {
  const me = useQuery({ queryKey: ["me"], queryFn: api.me, retry: false });
  const ready = useQuery({ queryKey: ["ready"], queryFn: api.ready, retry: false });

  if (me.isLoading || ready.isLoading) {
    return <p className="subtitle">Checking instance…</p>;
  }

  if (me.data) {
    return <Dashboard user={me.data} />;
  }

  // 409 already-initialized → login; otherwise offer bootstrap first.
  const gate: Gate = ready.error instanceof ApiError && ready.error.status === 503
    ? "bootstrap"
    : "login";
  return gate === "bootstrap" ? <Bootstrap /> : <Login />;
}

function Login() {
  const qc = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const login = useMutation({
    mutationFn: () => api.login(username, password),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });

  return (
    <div className="card">
      <h1>supabackup</h1>
      <p className="subtitle">Sign in to your instance</p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          login.mutate();
        }}
      >
        <label htmlFor="username">Username</label>
        <input id="username" value={username} autoComplete="username" onChange={(e) => setUsername(e.target.value)} required />
        <label htmlFor="password">Password</label>
        <input id="password" type="password" value={password} autoComplete="current-password" onChange={(e) => setPassword(e.target.value)} required />
        {login.isError && <p className="error">{(login.error as ApiError).message}</p>}
        <button type="submit" disabled={login.isPending}>
          {login.isPending ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </div>
  );
}

function Bootstrap() {
  const qc = useQueryClient();
  const [token, setToken] = useState("");
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const bootstrap = useMutation({
    mutationFn: () => api.bootstrap(token, username, password),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });

  return (
    <div className="card">
      <h1>Welcome to supabackup</h1>
      <p className="subtitle">
        This instance is not initialized yet. Run{" "}
        <code>docker exec &lt;container&gt; supabackup bootstrap</code> on the
        server to print a one-time token — the first visitor can never claim
        the admin account without it.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          bootstrap.mutate();
        }}
      >
        <label htmlFor="token">Bootstrap token</label>
        <input id="token" value={token} onChange={(e) => setToken(e.target.value)} required />
        <label htmlFor="b-username">Admin username</label>
        <input id="b-username" value={username} onChange={(e) => setUsername(e.target.value)} required />
        <label htmlFor="b-password">Password (min 12 characters)</label>
        <input id="b-password" type="password" value={password} autoComplete="new-password" onChange={(e) => setPassword(e.target.value)} required minLength={12} />
        {bootstrap.isError && <p className="error">{(bootstrap.error as ApiError).message}</p>}
        <button type="submit" disabled={bootstrap.isPending}>
          {bootstrap.isPending ? "Creating admin…" : "Create admin account"}
        </button>
      </form>
    </div>
  );
}

function Dashboard({ user }: { user: User }) {
  const qc = useQueryClient();
  const details = useQuery({ queryKey: ["health"], queryFn: api.healthDetails, refetchInterval: 10_000 });
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });

  return (
    <div>
      <div className="card">
        <h1>supabackup</h1>
        <p className="subtitle">
          Signed in as {user.username} · Phase 1 skeleton — databases, backups
          and storage destinations arrive in Phase 2–3.
        </p>
        {details.data && (
          <>
            <div className="kv"><span>Version</span><span>{details.data.version}</span></div>
            <div className="kv"><span>Commit</span><span>{details.data.commit}</span></div>
            <div className="kv"><span>Uptime</span><span>{Math.floor(details.data.uptimeSeconds / 60)} min</span></div>
          </>
        )}
        <button
          type="button"
          className="secondary"
          onClick={() => logout.mutate()}
          disabled={logout.isPending}
        >
          Sign out
        </button>
      </div>
    </div>
  );
}
