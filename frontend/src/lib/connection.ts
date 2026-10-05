// Client-side pre-flight for postgres:// connection strings. It mirrors the
// rules the backend enforces at registration (pgclient.ParseURI and
// platform.Detect / PoolingHint) so problems surface before the request,
// and prepares the string the way the server accepts it. The server stays
// the authority: anything this misses is still rejected or warned there.

export type Provider = "supabase" | "neon" | "railway" | "generic";

export const SSL_MODES = ["require", "verify-full", "verify-ca", "prefer", "allow", "disable"] as const;
export type SslMode = (typeof SSL_MODES)[number];

/** The only query parameters the backend accepts. */
const ALLOWED_PARAMS = ["sslmode", "connect_timeout", "application_name"];
const MAX_URI_LENGTH = 500;

export interface ConnectionFields {
  host: string;
  port: string;
  database: string;
  user: string;
  password: string;
}

/** A one-click correction: replaces the host and/or port of the target. */
export interface HostPatch {
  host?: string;
  port?: string;
}

export interface Finding {
  level: "error" | "warn" | "info" | "ok";
  text: string;
  fix?: { label: string; patch: HostPatch };
}

export interface Analysis {
  /** Platform inferred from the host; "generic" when it is not a known one. */
  platform: Provider;
  findings: Finding[];
  /** What will be sent: unsupported parameters dropped, TLS mode added. */
  normalized: string;
  /** Same target with the password masked, safe to display. */
  redacted: string;
  blocked: boolean;
}

interface Parts {
  scheme: string;
  user: string;
  password: string | null;
  host: string;
  port: string;
  path: string;
  query: string;
}

const hostSuffix = (host: string, suffix: string) => host === suffix || host.endsWith(`.${suffix}`);

export function detectProvider(host: string): Provider {
  const h = host.toLowerCase().replace(/\.$/, "");
  if (hostSuffix(h, "supabase.co") || hostSuffix(h, "supabase.com")) return "supabase";
  if (hostSuffix(h, "neon.tech")) return "neon";
  if (hostSuffix(h, "rlwy.net") || hostSuffix(h, "railway.internal")) return "railway";
  return "generic";
}

export function isLocalHost(host: string): boolean {
  return host === "localhost" || host === "::1" || /^127\.\d+\.\d+\.\d+$/.test(host);
}

const bracket = (host: string) => (host.includes(":") ? `[${host}]` : host);

// Split by hand rather than through URL(): the credentials must be passed on
// byte-for-byte, and URL() re-encodes them.
function split(raw: string): Parts | string {
  const m = /^(postgres(?:ql)?):\/\/(.*)$/i.exec(raw);
  if (!m) return "Must start with postgres:// or postgresql://";
  let rest = m[2];
  if (rest.includes("#")) {
    return "Contains “#”. If it is part of the password, write it as %23 — or switch to “Enter details”, which encodes it for you.";
  }
  let query = "";
  const q = rest.indexOf("?");
  if (q >= 0) {
    query = rest.slice(q + 1);
    rest = rest.slice(0, q);
  }
  const slash = rest.indexOf("/");
  const authority = slash >= 0 ? rest.slice(0, slash) : rest;
  const path = slash >= 0 ? rest.slice(slash) : "";
  const at = authority.lastIndexOf("@");
  if (at < 0) return "Missing the user (expected user:password@host).";
  const userinfo = authority.slice(0, at);
  const hostport = authority.slice(at + 1);
  const colon = userinfo.indexOf(":");
  const user = colon >= 0 ? userinfo.slice(0, colon) : userinfo;
  const password = colon >= 0 ? userinfo.slice(colon + 1) : null;
  if (user === "") return "Missing the user (expected user:password@host).";

  let host = hostport;
  let port = "";
  if (hostport.startsWith("[")) {
    const end = hostport.indexOf("]");
    if (end < 0) return "Unclosed “[” in the IPv6 host.";
    host = hostport.slice(1, end);
    port = hostport.slice(end + 1).replace(/^:/, "");
  } else {
    if (hostport.includes(",")) return "Multi-host lists are not supported — give one host.";
    const pc = hostport.lastIndexOf(":");
    if (pc >= 0) {
      host = hostport.slice(0, pc);
      port = hostport.slice(pc + 1);
    }
  }
  if (host === "") return "Missing the host.";
  if (port !== "" && !(/^\d+$/.test(port) && Number(port) >= 1 && Number(port) <= 65535)) {
    return `“${port}” is not a valid port. If the password contains “:”, “/” or “@”, switch to “Enter details”.`;
  }
  if (path === "" || path === "/") return "Missing the database name (…/postgres).";
  return { scheme: m[1], user, password, host, port, path, query };
}

function join(p: Parts, query: string): string {
  const auth = p.password === null ? p.user : `${p.user}:${p.password}`;
  const hostport = bracket(p.host) + (p.port ? `:${p.port}` : "");
  return `${p.scheme}://${auth}@${hostport}${p.path}${query ? `?${query}` : ""}`;
}

/** Apply a one-click host/port correction to a connection string. */
export function applyPatch(raw: string, patch: HostPatch): string {
  const p = split(raw.trim());
  if (typeof p === "string") return raw;
  return join({ ...p, host: patch.host ?? p.host, port: patch.port ?? p.port }, p.query);
}

export function buildUri(f: ConnectionFields, sslMode: SslMode): string {
  const enc = encodeURIComponent;
  const auth = f.password === "" ? enc(f.user.trim()) : `${enc(f.user.trim())}:${enc(f.password)}`;
  const port = f.port.trim() || "5432";
  return `postgresql://${auth}@${bracket(f.host.trim())}:${port}/${enc(f.database.trim())}?sslmode=${sslMode}`;
}

// Mirrors platform.PoolingHint, plus the cases the server does not flag.
function hostFindings(p: Parts): Finding[] {
  const host = p.host.toLowerCase().replace(/\.$/, "");
  const port = p.port || "5432";
  const provider = detectProvider(host);
  const out: Finding[] = [];

  if (provider === "supabase") {
    if (port === "6543") {
      out.push({
        level: "warn",
        text: "Port 6543 is Supabase’s transaction pooler. pg_dump cannot run through it, so backups would fail.",
        fix: { label: "Use the session port (5432)", patch: { port: "5432" } },
      });
    } else if (/^db\.[a-z0-9]+\.supabase\.co$/.test(host)) {
      out.push({
        level: "info",
        text: "This is the direct connection, which is IPv6-only unless the project has the IPv4 add-on. If the test cannot connect, use the Session pooler string instead.",
      });
    }
  }
  if (provider === "neon") {
    const [label, ...restLabels] = host.split(".");
    if (label.endsWith("-pooler")) {
      out.push({
        level: "warn",
        text: "This is Neon’s pooled endpoint (“-pooler” in the host). pg_dump cannot run through it on any port.",
        fix: {
          label: "Use the direct endpoint",
          patch: { host: [label.slice(0, -"-pooler".length), ...restLabels].join("."), port: "5432" },
        },
      });
    } else if (port === "6543") {
      out.push({
        level: "warn",
        text: "Port 6543 is Neon’s pooled port. pg_dump cannot run through it.",
        fix: { label: "Use port 5432", patch: { port: "5432" } },
      });
    }
  }
  if (hostSuffix(host, "railway.internal")) {
    out.push({
      level: "warn",
      text: "*.railway.internal only resolves inside Railway’s private network. Unless supabackup runs there too, use DATABASE_PUBLIC_URL (the *.proxy.rlwy.net TCP proxy).",
    });
  }
  if (isLocalHost(host)) {
    out.push({
      level: "info",
      text: "localhost is the supabackup container itself, not the machine running Docker. For a database on the Docker host use host.docker.internal or the host’s LAN address.",
    });
  }
  return out;
}

/**
 * Check a connection string and prepare what will be sent.
 * `sslMode` is used only when the string does not set one itself.
 */
export function analyze(rawInput: string, sslMode: SslMode): Analysis {
  const raw = rawInput.trim();
  const fail = (text: string): Analysis => ({
    platform: "generic",
    findings: [{ level: "error", text }],
    normalized: raw,
    redacted: "",
    blocked: true,
  });
  if (raw === "") return fail("Paste the connection string.");
  const p = split(raw);
  if (typeof p === "string") return fail(p);

  const findings: Finding[] = [];
  if (p.password !== null && /^\[.*\]$/.test(decodeSafe(p.password))) {
    findings.push({
      level: "error",
      text: `The password is still the placeholder ${decodeSafe(p.password)} — replace it with the real database password.`,
    });
  } else if (p.password === null || p.password === "") {
    findings.push({ level: "warn", text: "No password in the string. The connection test will fail unless the server allows that." });
  }

  findings.push(...hostFindings(p));

  // Query: keep what the server accepts, say what was dropped.
  const kept: string[] = [];
  const dropped: string[] = [];
  let explicitSsl: string | null = null;
  for (const pair of p.query.split("&").filter(Boolean)) {
    const key = decodeSafe(pair.split("=")[0]);
    if (!ALLOWED_PARAMS.includes(key)) {
      dropped.push(key);
      continue;
    }
    if (key === "sslmode") explicitSsl = decodeSafe(pair.slice(pair.indexOf("=") + 1));
    kept.push(pair);
  }
  if (dropped.length > 0) {
    findings.push({
      level: "info",
      text: `Removing ${dropped.map((k) => `“${k}”`).join(", ")} — supabackup only accepts sslmode, connect_timeout and application_name.`,
    });
  }
  if (explicitSsl === null) {
    kept.push(`sslmode=${sslMode}`);
    findings.push({ level: "info", text: `The string sets no TLS mode; adding sslmode=${sslMode} (chosen below).` });
  } else if (!(SSL_MODES as readonly string[]).includes(explicitSsl)) {
    findings.push({ level: "error", text: `“${explicitSsl}” is not a valid sslmode.` });
  }
  const effectiveSsl = explicitSsl ?? sslMode;
  if (effectiveSsl === "disable" && !isLocalHost(p.host)) {
    findings.push({
      level: "warn",
      text: "sslmode=disable sends the password and every dumped row unencrypted. Use it only on a private network you trust.",
    });
  }

  const query = kept.join("&");
  const normalized = join(p, query);
  if (normalized.length > MAX_URI_LENGTH) {
    findings.push({ level: "error", text: `Connection strings are limited to ${MAX_URI_LENGTH} characters.` });
  }
  if (!findings.some((f) => f.level === "error" || f.level === "warn")) {
    findings.unshift({ level: "ok", text: "Looks compatible with pg_dump. The connection is tested when you register." });
  }

  return {
    platform: detectProvider(p.host),
    findings,
    normalized,
    redacted: join({ ...p, password: p.password ? "••••••" : p.password }, query),
    blocked: findings.some((f) => f.level === "error"),
  };
}

function decodeSafe(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}
