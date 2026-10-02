import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError, type User } from "./api/client";

// Flow: me() resolves to an explicit `null` on 401 (never a stale cached
// user), so rendering is driven by real authentication state. A fresh
// instance offers the bootstrap form via an explicit UI entry on the login
// screen (review P1-10) — readiness is NOT used to infer initialization.

export default function App() {
  const me = useQuery<User | null, Error>({
    queryKey: ["me"],
    queryFn: api.me,
    retry: false,
    staleTime: 30_000,
  });
  const ready = useQuery({ queryKey: ["ready"], queryFn: api.ready, retry: false });

  if (me.isPending || ready.isPending) {
    return <p className="subtitle">Checking instance…</p>;
  }
  if (ready.isError) {
    return (
      <div className="card">
        <h1>supabackup</h1>
        <p className="error">Service unavailable — retrying…</p>
      </div>
    );
  }
  if (me.data) {
    return <Dashboard user={me.data} />;
  }
  return <Login />;
}

function Login() {
  const qc = useQueryClient();
  const [mode, setMode] = useState<"login" | "bootstrap">("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [token, setToken] = useState("");

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
      if (e instanceof ApiError && e.status === 409) setMode("login");
    },
  });

  if (mode === "bootstrap") {
    return (
      <div className="card">
        <h1>Welcome to supabackup</h1>
        <p className="subtitle">
          This instance is not initialized yet. Run{" "}
          <code>docker exec &lt;container&gt; /app/supabackup bootstrap</code>{" "}
          on the server to print a one-time token — the first visitor can never
          claim the admin account without it.
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
          <input
            id="b-password"
            type="password"
            value={password}
            autoComplete="new-password"
            onChange={(e) => setPassword(e.target.value)}
            required
            minLength={12}
          />
          {bootstrap.isError && <p className="error">{(bootstrap.error as ApiError).message}</p>}
          <button type="submit" disabled={bootstrap.isPending}>
            {bootstrap.isPending ? "Creating admin…" : "Create admin account"}
          </button>
        </form>
        <button type="button" className="secondary" onClick={() => setMode("login")}>
          Back to sign in
        </button>
      </div>
    );
  }

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
        <input
          id="password"
          type="password"
          value={password}
          autoComplete="current-password"
          onChange={(e) => setPassword(e.target.value)}
          required
        />
        {login.isError && <p className="error">{(login.error as ApiError).message}</p>}
        <button type="submit" disabled={login.isPending}>
          {login.isPending ? "Signing in…" : "Sign in"}
        </button>
      </form>
      <button type="button" className="secondary" onClick={() => setMode("bootstrap")}>
        Initialize a fresh instance with a CLI token
      </button>
    </div>
  );
}

function Dashboard({ user }: { user: User }) {
  const qc = useQueryClient();
  const details = useQuery({
    queryKey: ["health"],
    queryFn: api.healthDetails,
    refetchInterval: 10_000,
    retry: false,
  });
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      // Drop ALL authenticated cache immediately: a later 401 refetch must
      // not resurrect stale data (review P1-11).
      qc.setQueryData(["me"], null);
      qc.removeQueries({ queryKey: ["health"] });
      qc.invalidateQueries();
    },
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
