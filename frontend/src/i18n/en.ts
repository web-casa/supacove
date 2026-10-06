// Source of truth. Flat dotted keys; `key.one`/`key.other` are plural forms
// selected by a `count` variable. zh-CN.ts must define every key here (tsc).
const raw = {
  // ---- app shell ----------------------------------------------------------
  "app.checking": "Checking instance…",
  "app.serviceUnavailable": "Service unavailable",
  "app.backendDown":
    "The backend is not answering its readiness probe — retrying every 5s.",

  // ---- auth ---------------------------------------------------------------
  "auth.signIn.title": "Sign in",
  "auth.signIn.sub": "Sign in to your instance.",
  "auth.bootstrap.title": "Initialize this instance",
  "auth.bootstrap.sub":
    "Create the admin account with a one-time token. The first visitor can never claim the instance without it.",
  "auth.bootstrap.runOnServer": "Run on the server to print a token:",
  "auth.bootstrap.validFor": "Valid for 15 minutes, usable once.",
  "auth.field.token": "Bootstrap token",
  "auth.field.username": "Username",
  "auth.field.adminUsername": "Admin username",
  "auth.field.password": "Password",
  "auth.field.passwordHint": "Minimum {n} characters.",
  "auth.error.token": "Paste the token printed by the bootstrap command.",
  "auth.error.username": "Enter a username.",
  "auth.error.password": "Enter a password.",
  "auth.error.passwordShort": "Use at least {n} characters ({len} so far).",
  "auth.notice.adminExists":
    "This instance already has an admin account — sign in instead.",
  "auth.action.signIn": "Sign in",
  "auth.action.signingIn": "Signing in…",
  "auth.action.createAdmin": "Create admin account",
  "auth.action.creatingAdmin": "Creating admin…",
  "auth.action.backToSignIn": "Back to sign in",
  "auth.action.bootstrapEntry": "Initialize a fresh instance with a CLI token",

  // ---- navigation / common ------------------------------------------------
  "nav.overview": "Overview",
  "nav.backups": "Backups",
  "nav.notifications": "Notifications",
  "nav.sections": "Sections",
  "nav.signOut": "Sign out",
  "common.cancel": "Cancel",
  "common.save": "Save",
  "common.saving": "Saving…",
  "common.close": "Close",
  "common.retry": "Retry",
  "common.remove": "Remove",
  "common.done": "Done",
  "common.never": "never",
  "common.show": "Show",
  "common.hide": "Hide",
  "common.showLatest": "Show latest {n}",
  "common.showAll": "Show all {n}",
  "lang.switch": "Language",

  // ---- health summary -------------------------------------------------------
  "health.aria": "Protection health",
  "health.title": "Protection status",
  "health.lastSuccess": "Last successful backup",
  "health.refreshTip": "Refresh now (auto-refreshes every 30s)",
  "health.headline.none": "No databases registered yet",
  "health.headline.protectedOne": "Your database is protected",
  "health.headline.protectedAll": "All {n} databases are protected",
  "health.headline.attention.one": "{need} of {total} database needs attention",
  "health.headline.attention.other":
    "{need} of {total} databases need attention",
  "health.detail.none":
    "Register a database below to start taking encrypted backups.",
  "health.detail.expired": "{n} expired",
  "health.detail.never": "{n} never backed up",
  "health.detail.verifyFailed": "{n} failed restore verification",
  "health.detail.lastJobFailed": "{n} with a failed last job",
  "health.detail.allGood": "Every database has a fresh successful backup.",
  "health.detail.unknown": "Protection state is unknown.",
  "health.detail.loading": "Loading protection state…",
  "health.unavailable": "Overview unavailable: {msg}",
  "health.tile.protected": "Protected",
  "health.tile.expired": "Expired",
  "health.tile.never": "Never backed up",
  "health.tile.paused": "Paused",

  // ---- vitals strip ---------------------------------------------------------
  "vitals.aria": "Database vitals — {summary}",
  "vitals.paused": "paused",

  // ---- protection panel / database table ------------------------------------
  "panel.protection.title": "Protection overview",
  "panel.protection.desc":
    "Protection derives from the last successful backup; a failed retry never counts as fresh.",
  "panel.protection.add": "Add database",
  "panel.protection.empty.title": "No databases yet",
  "panel.protection.empty.cta": "Register your first database",
  "panel.protection.empty.body":
    "Connect a Supabase, Neon, Railway or any PostgreSQL database with its connection string. Backups are age-encrypted before they leave this server.",
  "table.aria.databases": "Databases",
  "table.col.database": "Database",
  "table.col.protection": "Protection",
  "table.col.verification": "Verification",
  "table.col.recentRuns": "Recent runs",
  "table.col.lastSuccess": "Last success",
  "table.col.actions": "Actions",

  // ---- database row ----------------------------------------------------------
  "dbrow.paused": "paused",
  "dbrow.noSuccess": "no successful backup yet",
  "dbrow.limit": " · limit {n}h",
  "dbrow.lastJobFailed": "last job FAILED — retry pending",
  "dbrow.backupState": "backup {state}…",
  "dbrow.backupNow": "Back up now",
  "dbrow.queueing": "Queueing…",
  "dbrow.scheduleTip": "Schedule & heartbeat",
  "dbrow.removeTip": "Remove database",
  "dbrow.removePrompt": "Remove “{name}”?",
  "dbrow.toast.queued": "Backup queued for “{name}”.",
  "dbrow.toast.removed": "Removed “{name}”.",

  // ---- run pulse ---------------------------------------------------------------
  "pulse.noRuns": "No runs yet",
  "pulse.lastRuns": "Last {n} runs: {list}",

  // ---- schedule form -------------------------------------------------------------
  "schedule.loading": "Loading schedule…",
  "schedule.unavailable": "Schedule unavailable: {msg}",
  "schedule.title": "Schedule & heartbeat",
  "schedule.lastScheduled": "Last scheduled run",
  "schedule.lastHeartbeat": "last heartbeat",
  "schedule.field.cron": "Cron schedule",
  "schedule.hint.cron": "5-field crontab; empty = manual only.",
  "schedule.field.tz": "Timezone (IANA)",
  "schedule.field.maxAge": "Freshness threshold (hours)",
  "schedule.hint.maxAge": "Older than this = EXPIRED. 0 = off.",
  "schedule.field.hbUrl": "Heartbeat URL",
  "schedule.hint.hbUrl":
    "Dead-man switch, optional. Empty inherits the server default; “-” disables.",
  "schedule.field.period": "Expected period (hours)",
  "schedule.field.grace": "Grace (hours)",
  "schedule.pause": "Pause scheduled backups",
  "schedule.pauseNote": " — manual “Back up now” keeps working.",
  "schedule.error.hoursWhole": "Use a whole number of hours, 0 or more.",
  "schedule.error.hoursMax": "At most {n} hours (one year).",
  "schedule.error.cron":
    "Use 5 fields: minute hour day-of-month month day-of-week.",
  "schedule.error.tz": "Not a known IANA timezone (e.g. Europe/Berlin).",
  "schedule.error.hbUrl":
    "Use an http:// or https:// URL, “-” to disable, or leave empty.",
  "schedule.error.period": "A heartbeat URL needs an expected period above 0.",
  "schedule.toast.saved": "Schedule saved for “{name}”.",
  "schedule.aria": "Schedule and heartbeat for {name}",

  // ---- register database ---------------------------------------------------------
  "reg.aria": "Register a database",
  "reg.title": "Register a database",
  "reg.desc":
    "The connection is tested before it is saved. The string is stored encrypted and never shown again.",
  "reg.providerAria": "Where is the database hosted?",
  "reg.field.name": "Display name",
  "reg.tab.aria": "How to enter the connection",
  "reg.tab.uri": "Paste connection string",
  "reg.tab.fields": "Enter details",
  "reg.field.uri": "postgres:// connection string",
  "reg.field.host": "Host",
  "reg.field.port": "Port",
  "reg.field.database": "Database",
  "reg.field.user": "Role",
  "reg.field.password": "Password",
  "reg.hint.password": "Special characters are encoded for you.",
  "reg.field.ssl": "TLS mode (sslmode)",
  "reg.hint.ssl":
    "require encrypts; verify-full also checks the server certificate against its hostname.",
  "reg.guide.title": "Where to find it — {label}",
  "reg.guide.aria": "{label} instructions",
  "reg.test": "Test & register",
  "reg.testing": "Testing connection…",
  "reg.registered": "Registered “{name}”.",
  "reg.registeredWithVersion": "Registered “{name}” — PostgreSQL {version}.",
  "reg.error.name": "Give this database a display name.",
  "reg.error.uri": "Paste the connection string.",
  "reg.error.host": "Enter the host.",
  "reg.error.port": "Port must be 1–65535.",
  "reg.error.database": "Enter the database name.",
  "reg.error.user": "Enter the role.",

  // ---- provider guidance -----------------------------------------------------------
  "providers.supabase.tagline": "Session pooler or direct",
  "providers.supabase.s1":
    "In the project dashboard open Connect, then the Connection string tab (type: URI).",
  "providers.supabase.s2":
    "Copy Session pooler (port 5432). It works over IPv4. Direct connection also works, but it is IPv6-only unless the project has the IPv4 add-on.",
  "providers.supabase.s3":
    "Do not use Transaction pooler (port 6543) — pg_dump cannot run through it.",
  "providers.supabase.s4":
    "Replace [YOUR-PASSWORD] with the database password.",
  "providers.neon.tagline": "Direct endpoint, pooling off",
  "providers.neon.s1":
    "In the Neon console open the project and click Connect.",
  "providers.neon.s2":
    "Switch Connection pooling off, so the host has no “-pooler” in it.",
  "providers.neon.s3":
    "Copy the string as-is. The channel_binding parameter Neon adds is removed here because supabackup does not accept it.",
  "providers.neon.s4":
    "An idle compute takes a few seconds to wake, so the first test can be slow.",
  "providers.railway.tagline": "Public TCP proxy URL",
  "providers.railway.s1": "Open the Postgres service, then the Variables tab.",
  "providers.railway.s2":
    "Copy DATABASE_PUBLIC_URL (host ends in .proxy.rlwy.net).",
  "providers.railway.s3":
    "Do not use DATABASE_URL: its *.railway.internal host only resolves inside Railway.",
  "providers.railway.s4":
    "Railway’s string sets no TLS mode, so one is added from the selector below.",
  "providers.generic.label": "Self-hosted / other",
  "providers.generic.tagline": "Any PostgreSQL server",
  "providers.generic.s1":
    "The host must be reachable from the supabackup container. localhost means the container itself; for the Docker host use host.docker.internal or its LAN address.",
  "providers.generic.s2":
    "Use a role that can read everything pg_dump exports: the database owner, or a member of pg_read_all_data (PostgreSQL 14+).",
  "providers.generic.s3":
    "TLS: verify-full when the server has a CA-signed certificate, require to encrypt without verifying, disable only on a private network you trust.",
  "providers.generic.s4":
    "Check pg_hba.conf allows this server’s address for that role and database.",

  // ---- connection pre-flight -----------------------------------------------------
  "preflight.aria": "Connection pre-flight",
  "preflight.willRegister": "Will register",
  "conn.err.empty": "Paste the connection string.",
  "conn.err.mustStart": "Must start with postgres:// or postgresql://",
  "conn.err.hash":
    "Contains “#”. If it is part of the password, write it as %23 — or switch to “Enter details”, which encodes it for you.",
  "conn.err.missingUser": "Missing the user (expected user:password@host).",
  "conn.err.unclosedV6": "Unclosed “[” in the IPv6 host.",
  "conn.err.multiHost": "Multi-host lists are not supported — give one host.",
  "conn.err.missingHost": "Missing the host.",
  "conn.err.badPort":
    "“{port}” is not a valid port. If the password contains “:”, “/” or “@”, switch to “Enter details”.",
  "conn.err.missingDb": "Missing the database name (…/postgres).",
  "conn.err.placeholderPassword":
    "The password is still the placeholder {ph} — replace it with the real database password.",
  "conn.err.badSsl": "“{mode}” is not a valid sslmode.",
  "conn.err.tooLong": "Connection strings are limited to {n} characters.",
  "conn.warn.noPassword":
    "No password in the string. The connection test will fail unless the server allows that.",
  "conn.warn.sbTxPool":
    "Port 6543 is Supabase’s transaction pooler. pg_dump cannot run through it, so backups would fail.",
  "conn.warn.neonPooled":
    "This is Neon’s pooled endpoint (“-pooler” in the host). pg_dump cannot run through it on any port.",
  "conn.warn.neonPort":
    "Port 6543 is Neon’s pooled port. pg_dump cannot run through it.",
  "conn.warn.railwayInternal":
    "*.railway.internal only resolves inside Railway’s private network. Unless supabackup runs there too, use DATABASE_PUBLIC_URL (the *.proxy.rlwy.net TCP proxy).",
  "conn.warn.sslDisable":
    "sslmode=disable sends the password and every dumped row unencrypted. Use it only on a private network you trust.",
  "conn.info.sbDirect":
    "This is the direct connection, which is IPv6-only unless the project has the IPv4 add-on. If the test cannot connect, use the Session pooler string instead.",
  "conn.info.localhost":
    "localhost is the supabackup container itself, not the machine running Docker. For a database on the Docker host use host.docker.internal or the host’s LAN address.",
  "conn.info.droppedParams":
    "Removing {params} — supabackup only accepts sslmode, connect_timeout and application_name.",
  "conn.info.addSsl":
    "The string sets no TLS mode; adding sslmode={mode} (chosen below).",
  "conn.ok.compatible":
    "Looks compatible with pg_dump. The connection is tested when you register.",
  "conn.fix.sbSessionPort": "Use the session port (5432)",
  "conn.fix.neonDirect": "Use the direct endpoint",
  "conn.fix.port5432": "Use port 5432",

  // ---- pipeline & statistics -----------------------------------------------------
  "pipe.title": "Pipeline & statistics",
  "pipe.desc":
    "Every backup takes this path. Rings show lifetime success per stage.",
  "pipe.aria": "Backup pipeline stages",
  "pipe.inFlight": "{n} in flight",
  "pipe.idle": "idle",
  "pipe.unavailable": "Statistics unavailable: {msg}",
  "pipe.stage.source": "Source",
  "pipe.stage.source.hint": "Newest known physical size per database, summed",
  "pipe.stage.source.sub.one": "{size} · {count} database",
  "pipe.stage.source.sub.other": "{size} · {count} databases",
  "pipe.stage.export": "Export",
  "pipe.stage.export.hint":
    "pg_dump success rate; size = compressed archives recorded in statistics",
  "pipe.stage.export.sub": "{size} dump archive",
  "pipe.stage.encrypt": "Encrypt",
  "pipe.stage.encrypt.hint": "age ciphertext across succeeded backups",
  "pipe.stage.encrypt.sub": "{size} encrypted",
  "pipe.stage.remote": "Remote commit",
  "pipe.stage.remote.hint": "Remote commits vs upload failures",
  "pipe.stage.remote.sub.one": "{count} destination",
  "pipe.stage.remote.sub.other": "{count} destinations",
  "pipe.stage.verify": "Verification",
  "pipe.stage.verify.hint": "Verified vs failed/unsupported restores",
  "pipe.stage.verify.sub": "restore-tested",
  "pipe.stage.notify": "Notification",
  "pipe.stage.notify.hint": "Delivered vs dead webhook deliveries",
  "pipe.stage.notify.sub": "webhook delivery",
  "pipe.stat.success": "Success rate",
  "pipe.stat.success.hint": "Succeeded / finished jobs",
  "pipe.stat.jobs": "Total jobs",
  "pipe.stat.succeeded": "Succeeded",
  "pipe.stat.failed": "Failed",
  "pipe.stat.avg": "Avg duration",
  "pipe.stat.avg.hint": "Dump through remote commit, succeeded jobs",
  "pipe.stat.lastSuccess": "Last success",

  // ---- recent backups ------------------------------------------------------------
  "backups.title": "Recent backups",
  "backups.desc": "Newest first. Refreshes every 15s.",
  "backups.aria": "Backup tasks",
  "backups.unavailable": "Backups unavailable: {msg}",
  "backups.empty.title": "No backups yet",
  "backups.empty.body":
    "Run “Back up now” on a database in the overview, or give it a cron schedule. Every run shows up here with its verification result and downloads.",
  "backups.col.backup": "Backup",
  "backups.col.status": "Status",
  "backups.col.checks": "Checks",
  "backups.col.size": "Size",
  "backups.col.when": "When",
  "backups.col.downloads": "Downloads",
  "backups.dbFallback": "database {id}",
  "backups.attempt": "attempt {n}",
  "backups.badge.remote": "remote",
  "backups.badge.uploading": "uploading",
  "backups.kit": "Recovery kit",
  "backups.bucketDl": "Download (bucket)",
  "backups.download": "Download",
  "backups.howToFix": "How to fix",
  "backups.linkError": "Could not get a download link: {msg}",

  // ---- webhooks ---------------------------------------------------------------------
  "wh.title": "Notifications",
  "wh.desc":
    "Webhooks receive backup_failed / backup_expired / verification_failed events with retries.",
  "wh.aria": "Webhooks",
  "wh.unavailable": "Webhooks unavailable: {msg}",
  "wh.add": "Add webhook",
  "wh.empty.title": "No webhooks yet",
  "wh.empty.cta": "Add your first webhook",
  "wh.empty.body":
    "Without a webhook, a failed or expired backup is only visible on this page. Point one at Slack, Discord or your pager.",
  "wh.col.name": "Name",
  "wh.col.events": "Events",
  "wh.col.url": "URL",
  "wh.sendTest": "Send test",
  "wh.removeTip": "Remove webhook",
  "wh.removePrompt": "Remove “{name}”?",
  "wh.toast.added": "Webhook “{name}” added.",
  "wh.toast.removed": "Removed webhook “{name}”.",
  "wh.addForm.desc":
    "A JSON POST is sent for each selected event, with retries.",
  "wh.addForm.aria": "Add webhook",
  "wh.field.name": "Name",
  "wh.field.url": "Webhook URL (http/https)",
  "wh.fieldset.events": "Events",
  "wh.testThisUrl": "Test this URL",
  "wh.test.delivered": "Test delivered.",
  "wh.test.failed": "Test failed: {msg}",
  "wh.test.unknown": "unknown",
  "wh.error.name": "Name this webhook.",
  "wh.error.url": "Enter the webhook URL.",
  "wh.error.urlScheme": "Must be an http:// or https:// URL.",
  "wh.error.events": "Pick at least one event.",

  // ---- delivery log -------------------------------------------------------------------
  "dl.title": "Delivery log",
  "dl.desc": "Outbox state for every alert sent to your webhooks.",
  "dl.aria": "Notification deliveries",
  "dl.unavailable": "Delivery log unavailable: {msg}",
  "dl.empty.title": "Nothing sent yet",
  "dl.empty.body":
    "Alerts appear here once a backup fails, expires or fails verification.",
  "dl.col.state": "State",
  "dl.col.event": "Event",
  "dl.col.database": "Database",
  "dl.col.attempts": "Attempts",
  "dl.col.created": "Created",

  // ---- status labels (lib/status.ts) --------------------------------------------------
  "status.protection.fresh": "protected",
  "status.protection.expired": "EXPIRED",
  "status.protection.never": "never backed up",
  "status.verify.verified": "restore-verified",
  "status.verify.failed": "verify FAILED",
  "status.verify.unsupported": "verify unsupported",
  "status.verify.pending": "verify pending…",
  "status.verify.running": "verify running…",
  "status.verify.skipped": "not verified",
  "status.task.succeeded": "succeeded",
  "status.task.failed": "failed",
  "status.task.running": "running",
  "status.task.pending": "pending",
  "status.task.interrupted": "interrupted",
  "status.task.canceled": "canceled",
  "status.delivery.delivered": "delivered",
  "status.delivery.dead": "dead",
  "status.delivery.delivering": "delivering",
  "status.delivery.pending": "pending",

  // ---- formatting helpers ----------------------------------------------------------------
  "ui.loading": "Loading",
  "fmt.requestFailed": "Request failed",
};

/** Every locale must translate exactly these keys. */
export type Dict = Record<keyof typeof raw, string>;

export const en: Dict = raw;
