// Client-side pre-flight for postgres:// connection strings. It mirrors the
// rules the backend enforces at registration (pgclient.ParseURI and
// platform.Detect / PoolingHint) so problems surface before the request,
// and prepares the string the way the server accepts it. The server stays
// the authority: anything this misses is still rejected or warned there.
// All human-readable copy lives in i18n dictionaries: findings carry a
// translation key plus interpolation variables.

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

/** Localizable message: an i18n key plus its interpolation variables. */
export interface Message {
  key: string;
  vars?: Record<string, string | number>;
}

export interface Finding extends Message {
  level: "error" | "warn" | "info" | "ok";
  fix?: { labelKey: string; patch: HostPatch };
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
function split(raw: string): Parts | Message {
  const m = /^(postgres(?:ql)?):\/\/(.*)$/i.exec(raw);
  if (!m) return { key: "conn.err.mustStart" };
  let rest = m[2];
  if (rest.includes("#")) {
    return { key: "conn.err.hash" };
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
  if (at < 0) return { key: "conn.err.missingUser" };
  const userinfo = authority.slice(0, at);
  const hostport = authority.slice(at + 1);
  const colon = userinfo.indexOf(":");
  const user = colon >= 0 ? userinfo.slice(0, colon) : userinfo;
  const password = colon >= 0 ? userinfo.slice(colon + 1) : null;
  if (user === "") return { key: "conn.err.missingUser" };

  let host = hostport;
  let port = "";
  if (hostport.startsWith("[")) {
    const end = hostport.indexOf("]");
    if (end < 0) return { key: "conn.err.unclosedV6" };
    host = hostport.slice(1, end);
    port = hostport.slice(end + 1).replace(/^:/, "");
  } else {
    if (hostport.includes(",")) return { key: "conn.err.multiHost" };
    const pc = hostport.lastIndexOf(":");
    if (pc >= 0) {
      host = hostport.slice(0, pc);
      port = hostport.slice(pc + 1);
    }
  }
  if (host === "") return { key: "conn.err.missingHost" };
  if (port !== "" && !(/^\d+$/.test(port) && Number(port) >= 1 && Number(port) <= 65535)) {
    return { key: "conn.err.badPort", vars: { port } };
  }
  if (path === "" || path === "/") return { key: "conn.err.missingDb" };
  return { scheme: m[1], user, password, host, port, path, query };
}

const isParts = (p: Parts | Message): p is Parts => "scheme" in p;

function join(p: Parts, query: string): string {
  const auth = p.password === null ? p.user : `${p.user}:${p.password}`;
  const hostport = bracket(p.host) + (p.port ? `:${p.port}` : "");
  return `${p.scheme}://${auth}@${hostport}${p.path}${query ? `?${query}` : ""}`;
}

/** Apply a one-click host/port correction to a connection string. */
export function applyPatch(raw: string, patch: HostPatch): string {
  const p = split(raw.trim());
  if (!isParts(p)) return raw;
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
        key: "conn.warn.sbTxPool",
        fix: { labelKey: "conn.fix.sbSessionPort", patch: { port: "5432" } },
      });
    } else if (/^db\.[a-z0-9]+\.supabase\.co$/.test(host)) {
      out.push({
        level: "info",
        key: "conn.info.sbDirect",
      });
    }
  }
  if (provider === "neon") {
    const [label, ...restLabels] = host.split(".");
    if (label.endsWith("-pooler")) {
      out.push({
        level: "warn",
        key: "conn.warn.neonPooled",
        fix: {
          labelKey: "conn.fix.neonDirect",
          patch: { host: [label.slice(0, -"-pooler".length), ...restLabels].join("."), port: "5432" },
        },
      });
    } else if (port === "6543") {
      out.push({
        level: "warn",
        key: "conn.warn.neonPort",
        fix: { labelKey: "conn.fix.port5432", patch: { port: "5432" } },
      });
    }
  }
  if (hostSuffix(host, "railway.internal")) {
    out.push({
      level: "warn",
      key: "conn.warn.railwayInternal",
    });
  }
  if (isLocalHost(host)) {
    out.push({
      level: "info",
      key: "conn.info.localhost",
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
  const fail = (msg: Message): Analysis => ({
    platform: "generic",
    findings: [{ level: "error", ...msg }],
    normalized: raw,
    redacted: "",
    blocked: true,
  });
  if (raw === "") return fail({ key: "conn.err.empty" });
  const p = split(raw);
  if (!isParts(p)) return fail(p);

  const findings: Finding[] = [];
  if (p.password !== null && /^\[.*\]$/.test(decodeSafe(p.password))) {
    findings.push({
      level: "error",
      key: "conn.err.placeholderPassword",
      vars: { ph: decodeSafe(p.password) },
    });
  } else if (p.password === null || p.password === "") {
    findings.push({ level: "warn", key: "conn.warn.noPassword" });
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
      key: "conn.info.droppedParams",
      vars: { params: dropped.map((k) => `“${k}”`).join(", ") },
    });
  }
  if (explicitSsl === null) {
    kept.push(`sslmode=${sslMode}`);
    findings.push({ level: "info", key: "conn.info.addSsl", vars: { mode: sslMode } });
  } else if (!(SSL_MODES as readonly string[]).includes(explicitSsl)) {
    findings.push({ level: "error", key: "conn.err.badSsl", vars: { mode: explicitSsl } });
  }
  const effectiveSsl = explicitSsl ?? sslMode;
  if (effectiveSsl === "disable" && !isLocalHost(p.host)) {
    findings.push({
      level: "warn",
      key: "conn.warn.sslDisable",
    });
  }

  const query = kept.join("&");
  const normalized = join(p, query);
  if (normalized.length > MAX_URI_LENGTH) {
    findings.push({ level: "error", key: "conn.err.tooLong", vars: { n: MAX_URI_LENGTH } });
  }
  if (!findings.some((f) => f.level === "error" || f.level === "warn")) {
    findings.unshift({ level: "ok", key: "conn.ok.compatible" });
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
