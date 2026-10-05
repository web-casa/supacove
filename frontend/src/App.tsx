import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import {
  api,
  ApiError,
  artifactDownloadPath,
  kitDownloadPath,
  presignedUrl,
  type DatabaseCreate,
  type OverviewEntry,
  type ScheduleConfig,
  type WebhookCreate,
} from "./api/client";
import type { User } from "./api/client";

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
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      // Drop ALL authenticated cache immediately: a later 401 refetch must
      // not resurrect stale data (review P1-11).
      qc.setQueryData(["me"], null);
      qc.removeQueries();
    },
  });

  return (
    <div>
      <div className="card">
        <h1>supabackup</h1>
        <p className="subtitle">
          Signed in as {user.username} — protection overview refreshes every 30s.
        </p>
        <button type="button" className="secondary" onClick={() => logout.mutate()} disabled={logout.isPending}>
          Sign out
        </button>
      </div>
      <OverviewSection />
      <AddDatabaseSection />
      <TasksSection />
      <WebhooksSection />
      <NotificationsSection />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview: which databases lack a fresh, verified, successful backup.
// ---------------------------------------------------------------------------

type EventType = "backup_failed" | "backup_expired" | "verification_failed";
const EVENT_TYPES: EventType[] = ["backup_failed", "backup_expired", "verification_failed"];

const stateLabel: Record<string, { text: string; cls: string }> = {
  fresh: { text: "protected", cls: "badge ok" },
  expired: { text: "EXPIRED", cls: "badge danger" },
  never: { text: "never backed up", cls: "badge warn" },
};

function verifyLabel(v?: string): { text: string; cls: string } | null {
  if (!v) return null;
  switch (v) {
    case "verified":
      return { text: "restore-verified", cls: "badge ok" };
    case "failed":
      return { text: "verify FAILED", cls: "badge danger" };
    case "unsupported":
      return { text: "verify unsupported", cls: "badge warn" };
    case "pending":
    case "running":
      return { text: `verify ${v}…`, cls: "badge" };
    case "skipped":
      return { text: "not verified", cls: "badge" };
    default:
      return null;
  }
}

function ageHours(h: number): string {
  if (h < 1) return `${Math.round(h * 60)} min ago`;
  if (h < 48) return `${h.toFixed(1)} h ago`;
  return `${(h / 24).toFixed(1)} d ago`;
}

function OverviewSection() {
  const qc = useQueryClient();
  const overview = useQuery({ queryKey: ["overview"], queryFn: api.overview, refetchInterval: 30_000 });
  const backupNow = useMutation({
    mutationFn: (id: number) => api.backupNow(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["overview"] }),
  });
  const [scheduleFor, setScheduleFor] = useState<number | null>(null);

  if (overview.isPending) return <div className="card"><p className="subtitle">Loading overview…</p></div>;
  if (overview.isError) {
    return <div className="card"><p className="error">Overview unavailable: {(overview.error as Error).message}</p></div>;
  }
  const dbs = overview.data.databases;
  return (
    <div className="card">
      <h1>Protection overview</h1>
      {dbs.length === 0 && <p className="subtitle">No databases registered yet — add one below.</p>}
      {dbs.map((d) => (
        <DatabaseRow
          key={d.databaseId}
          entry={d}
          onBackup={() => backupNow.mutate(d.databaseId)}
          backupPending={backupNow.isPending && backupNow.variables === d.databaseId}
          onConfigure={() => setScheduleFor(scheduleFor === d.databaseId ? null : d.databaseId)}
        />
      ))}
      {dbs.some((d) => scheduleFor === d.databaseId) && null}
      {scheduleFor !== null && <ScheduleForm databaseId={scheduleFor} onDone={() => setScheduleFor(null)} />}
    </div>
  );
}

function DatabaseRow({
  entry,
  onBackup,
  backupPending,
  onConfigure,
}: {
  entry: OverviewEntry;
  onBackup: () => void;
  backupPending: boolean;
  onConfigure: () => void;
}) {
  const qc = useQueryClient();
  const del = useMutation({
    mutationFn: () => api.deleteDatabase(entry.databaseId),
    onSuccess: () => qc.invalidateQueries(),
  });
  const state = stateLabel[entry.state] ?? { text: entry.state, cls: "badge" };
  const verify = verifyLabel(entry.lastSuccessVerifyStatus);
  const failedRetry = entry.lastJobStatus === "failed";
  return (
    <div className="db-row">
      <div className="db-head">
        <strong>{entry.name}</strong>
        <span className={state.cls}>{state.text}</span>
        {verify && <span className={verify.cls}>{verify.text}</span>}
        {entry.schedulePaused && <span className="badge">paused</span>}
      </div>
      <div className="db-meta">
        {entry.state === "never" ? (
          <span className="muted">no successful backup yet</span>
        ) : (
          <span className="muted">
            last success {entry.lastSuccessAgeHours != null && ` ${ageHours(entry.lastSuccessAgeHours)}`}
            {entry.lastSuccessAt != null && ` (${new Date(entry.lastSuccessAt * 1000).toLocaleString()})`}
          </span>
        )}
        {failedRetry && <span className="error-inline">last job FAILED — retry pending</span>}
      </div>
      <div className="db-actions">
        <button type="button" onClick={onBackup} disabled={backupPending}>
          {backupPending ? "Queueing…" : "Back up now"}
        </button>
        {entry.lastSuccessVerifyStatus && (
          <a className="secondary" href={kitDownloadPath(0)} style={{ display: "none" }} aria-hidden>
            noop
          </a>
        )}
        <button type="button" className="secondary" onClick={onConfigure}>
          Schedule &amp; heartbeat
        </button>
        <button type="button" className="secondary danger-btn" onClick={() => del.mutate()} disabled={del.isPending}>
          {del.isPending ? "Deleting…" : "Remove"}
        </button>
      </div>
    </div>
  );
}

function ScheduleForm({ databaseId, onDone }: { databaseId: number; onDone: () => void }) {
  const qc = useQueryClient();
  const existing = useQuery({
    queryKey: ["schedule", databaseId],
    queryFn: () => api.getSchedule(databaseId),
  });
  const [form, setForm] = useState<Partial<ScheduleConfig>>({});
  const save = useMutation({
    mutationFn: () =>
      api.putSchedule(databaseId, {
        cronExpr: form.cronExpr ?? existing.data?.cronExpr ?? "",
        cronTz: form.cronTz ?? existing.data?.cronTz ?? "UTC",
        maxAgeHours: form.maxAgeHours ?? existing.data?.maxAgeHours ?? 0,
        paused: form.paused ?? existing.data?.paused ?? false,
        heartbeatUrl: form.heartbeatUrl ?? existing.data?.heartbeatUrl ?? "",
        heartbeatPeriodHours: form.heartbeatPeriodHours ?? existing.data?.heartbeatPeriodHours ?? 0,
        heartbeatGraceHours: form.heartbeatGraceHours ?? existing.data?.heartbeatGraceHours ?? 0,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["overview"] });
      onDone();
    },
  });
  if (existing.isPending) return <p className="subtitle">Loading schedule…</p>;
  const cur = existing.data;
  const value = <K extends keyof ScheduleConfig>(k: K): ScheduleConfig[K] => form[k] ?? (cur?.[k] as ScheduleConfig[K]);
  return (
    <form
      className="schedule-form"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <label htmlFor={`cron-${databaseId}`}>Cron schedule (5-field, empty = manual only)</label>
      <input
        id={`cron-${databaseId}`}
        placeholder="0 3 * * *"
        value={value("cronExpr") ?? ""}
        onChange={(e) => setForm((f) => ({ ...f, cronExpr: e.target.value }))}
      />
      <label htmlFor={`tz-${databaseId}`}>Timezone (IANA)</label>
      <input id={`tz-${databaseId}`} value={value("cronTz") ?? "UTC"} onChange={(e) => setForm((f) => ({ ...f, cronTz: e.target.value }))} />
      <label htmlFor={`maxage-${databaseId}`}>Freshness threshold (hours; 0 = off)</label>
      <input
        id={`maxage-${databaseId}`}
        type="number"
        min={0}
        value={value("maxAgeHours") ?? 0}
        onChange={(e) => setForm((f) => ({ ...f, maxAgeHours: Number(e.target.value) }))}
      />
      <label htmlFor={`hb-${databaseId}`}>Heartbeat URL (dead-man switch; optional)</label>
      <input
        id={`hb-${databaseId}`}
        placeholder="https://hc-ping.com/…"
        value={value("heartbeatUrl") ?? ""}
        onChange={(e) => setForm((f) => ({ ...f, heartbeatUrl: e.target.value }))}
      />
      <div className="grid2">
        <div>
          <label htmlFor={`hbp-${databaseId}`}>Expected period (h)</label>
          <input
            id={`hbp-${databaseId}`}
            type="number"
            min={0}
            value={value("heartbeatPeriodHours") ?? 0}
            onChange={(e) => setForm((f) => ({ ...f, heartbeatPeriodHours: Number(e.target.value) }))}
          />
        </div>
        <div>
          <label htmlFor={`hbg-${databaseId}`}>Grace (h)</label>
          <input
            id={`hbg-${databaseId}`}
            type="number"
            min={0}
            value={value("heartbeatGraceHours") ?? 0}
            onChange={(e) => setForm((f) => ({ ...f, heartbeatGraceHours: Number(e.target.value) }))}
          />
        </div>
      </div>
      <label className="check">
        <input
          type="checkbox"
          checked={value("paused") ?? false}
          onChange={(e) => setForm((f) => ({ ...f, paused: e.target.checked }))}
        />
        Pause scheduled backups
      </label>
      {save.isError && <p className="error">{(save.error as ApiError).message}</p>}
      <div className="db-actions">
        <button type="submit" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save"}
        </button>
        <button type="button" className="secondary" onClick={onDone}>
          Cancel
        </button>
      </div>
    </form>
  );
}

// ---------------------------------------------------------------------------
// Database registration with the pooling warning surfaced (P2-01).
// ---------------------------------------------------------------------------

function AddDatabaseSection() {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [uri, setUri] = useState("");
  const [warning, setWarning] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: (body: DatabaseCreate) => api.createDatabase(body),
    onSuccess: (db) => {
      setWarning(db.poolingWarning ?? null);
      setName("");
      setUri("");
      qc.invalidateQueries();
    },
  });
  return (
    <div className="card">
      <h1>Register a database</h1>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate({ name, platform: "generic", connectionUri: uri });
        }}
      >
        <label htmlFor="db-name">Display name</label>
        <input id="db-name" value={name} onChange={(e) => setName(e.target.value)} required maxLength={100} />
        <label htmlFor="db-uri">postgres:// connection string</label>
        <input
          id="db-uri"
          type="password"
          placeholder="postgresql://user@host:5432/db?sslmode=require"
          value={uri}
          onChange={(e) => setUri(e.target.value)}
          required
          minLength={20}
        />
        {warning && <p className="warning">{warning}</p>}
        {create.isError && <p className="error">{(create.error as ApiError).message}</p>}
        <button type="submit" disabled={create.isPending}>
          {create.isPending ? "Testing connection…" : "Test & register"}
        </button>
      </form>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Recent tasks with kit/artifact downloads (kit = Phase 5 recovery kit).
// ---------------------------------------------------------------------------

function TasksSection() {
  const tasks = useQuery({ queryKey: ["tasks"], queryFn: api.tasks, refetchInterval: 15_000 });
  if (tasks.isError) return null;
  const list = tasks.data?.tasks ?? [];
  return (
    <div className="card">
      <h1>Recent backups</h1>
      {list.length === 0 && <p className="subtitle">No backups yet.</p>}
      {list.slice(0, 10).map((t) => (
        <div className="db-row" key={t.id}>
          <div className="db-head">
            <span className="muted">#{t.id}</span>
            <span className="badge">{t.status}</span>
            {t.verifyStatus === "verified" && <span className="badge ok">verified</span>}
            {t.verifyStatus === "failed" && <span className="badge danger">verify failed</span>}
            {(t as { remoteState?: string }).remoteState === "committed" && <span className="badge">remote</span>}
          </div>
          <div className="db-actions">
            {t.status === "succeeded" && t.hasRecoveryKit && (
              <a className="secondary" href={kitDownloadPath(t.id)}>
                recovery kit
              </a>
            )}
            {t.status === "succeeded" && (t as { remoteState?: string }).remoteState === "committed" && (
              <button
                type="button"
                className="secondary"
                onClick={() => presignedUrl(t.id).then((u) => window.open(u, "_blank"))}
              >
                download (bucket)
              </button>
            )}
            {t.status === "succeeded" && t.remoteState !== "committed" && (
              <a className="secondary" href={artifactDownloadPath(t.id)}>
                download
              </a>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Webhooks: list / add / test / remove.
// ---------------------------------------------------------------------------

function WebhooksSection() {
  const qc = useQueryClient();
  const hooks = useQuery({ queryKey: ["webhooks"], queryFn: api.webhooks });
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [events, setEvents] = useState<EventType[]>(["backup_failed", "backup_expired"]);
  const [testResult, setTestResult] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: (body: WebhookCreate) => api.createWebhook(body),
    onSuccess: () => {
      setName("");
      setUrl("");
      qc.invalidateQueries({ queryKey: ["webhooks"] });
    },
  });
  const test = useMutation({
    mutationFn: (body: WebhookCreate) => api.testWebhook(body),
    onSuccess: (r) => setTestResult(r.delivered ? "Test delivered." : `Failed: ${r.detail ?? "unknown"}`),
    onError: (e) => setTestResult(`Failed: ${(e as ApiError).message}`),
  });
  const del = useMutation({
    mutationFn: (id: number) => api.deleteWebhook(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["webhooks"] }),
  });

  const toggleEvent = (ev: EventType) =>
    setEvents((cur) => (cur.includes(ev) ? cur.filter((x) => x !== ev) : [...cur, ev]));

  return (
    <div className="card">
      <h1>Notifications</h1>
      <p className="subtitle">Webhooks receive backup_failed / backup_expired / verification_failed events with retries.</p>
      {(hooks.data?.webhooks ?? []).map((w) => (
        <div className="db-row" key={w.id}>
          <div className="db-head">
            <strong>{w.name}</strong>
            <span className="muted">{w.events?.join(", ")}</span>
          </div>
          <div className="db-meta">
            <span className="muted">{w.url}</span>
          </div>
          <div className="db-actions">
            <button
              type="button"
              className="secondary"
              onClick={() => test.mutate({ name: w.name, url: w.url, events: [...(w.events ?? [])] })}
              disabled={test.isPending}
            >
              Send test
            </button>
            <button type="button" className="secondary danger-btn" onClick={() => del.mutate(w.id)}>
              Remove
            </button>
          </div>
        </div>
      ))}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate({ name, url, events });
        }}
      >
        <label htmlFor="wh-name">Name</label>
        <input id="wh-name" value={name} onChange={(e) => setName(e.target.value)} required maxLength={100} />
        <label htmlFor="wh-url">Webhook URL (http/https)</label>
        <input id="wh-url" type="password" value={url} onChange={(e) => setUrl(e.target.value)} required minLength={8} />
        <div className="check-row">
          {EVENT_TYPES.map((ev) => (
            <label className="check" key={ev}>
              <input type="checkbox" checked={events.includes(ev)} onChange={() => toggleEvent(ev)} />
              {ev}
            </label>
          ))}
        </div>
        {create.isError && <p className="error">{(create.error as ApiError).message}</p>}
        <div className="db-actions">
          <button type="submit" disabled={create.isPending}>
            Add webhook
          </button>
          <button
            type="button"
            className="secondary"
            onClick={() => test.mutate({ name: name || "test", url, events })}
            disabled={test.isPending || url === ""}
          >
            Test this URL
          </button>
        </div>
        {testResult && <p className="subtitle">{testResult}</p>}
      </form>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Notification outbox delivery states.
// ---------------------------------------------------------------------------

function NotificationsSection() {
  const notifications = useQuery({ queryKey: ["notifications"], queryFn: api.notifications, refetchInterval: 20_000 });
  if (notifications.isError) return null;
  const list = notifications.data?.notifications ?? [];
  if (list.length === 0) return null;
  return (
    <div className="card">
      <h1>Delivery log</h1>
      {list.slice(0, 10).map((n) => (
        <div className="db-row" key={n.id}>
          <div className="db-head">
            <span className="badge">{n.state}</span>
            <strong>{n.eventType}</strong>
            <span className="muted">{n.databaseName}</span>
          </div>
          {n.lastError && <div className="db-meta"><span className="error-inline">{n.lastError}</span></div>}
        </div>
      ))}
    </div>
  );
}
