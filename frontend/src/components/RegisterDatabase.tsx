import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Eye, EyeOff, PlugZap } from "lucide-react";
import { api, type Database, type DatabaseCreate } from "../api/client";
import {
  analyze,
  applyPatch,
  buildUri,
  detectProvider,
  isLocalHost,
  SSL_MODES,
  type ConnectionFields,
  type HostPatch,
  type Provider,
  type SslMode,
} from "../lib/connection";
import { control, shown } from "../lib/form";
import { errorMessage } from "../lib/format";
import { PROVIDERS, providerInfo } from "../lib/providers";
import { ConnectionPreflight } from "./ConnectionPreflight";
import { Button } from "./ui/Button";
import { Field } from "./ui/Field";
import { InlineMessage } from "./ui/InlineMessage";

const EMPTY_FIELDS: ConnectionFields = { host: "", port: "5432", database: "", user: "", password: "" };
const HOST_IN_URI = /@([^/?#]+?)(?::\d+)?(?:[/?#]|$)/;

// Database registration, guided per platform. The connection string is
// checked and prepared client-side (lib/connection.ts) so pooled endpoints,
// missing TLS modes and unsupported parameters are caught before the request;
// the server's own poolingWarning is still surfaced afterwards (P2-01).
export function RegisterDatabase({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [provider, setProvider] = useState<Provider>("supabase");
  const [mode, setMode] = useState<"uri" | "fields">("uri");
  const [name, setName] = useState("");
  const [uri, setUri] = useState("");
  const [fields, setFields] = useState<ConnectionFields>(EMPTY_FIELDS);
  const [sslMode, setSslMode] = useState<SslMode>("require");
  const [reveal, setReveal] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [registered, setRegistered] = useState<Database | null>(null);

  const create = useMutation({
    mutationFn: (body: DatabaseCreate) => api.createDatabase(body),
    onSuccess: (db) => {
      setRegistered(db);
      setName("");
      setUri("");
      setFields(EMPTY_FIELDS);
      setSubmitted(false);
      qc.invalidateQueries();
    },
  });

  const info = providerInfo(provider);
  const fieldsComplete = fields.host.trim() !== "" && fields.database.trim() !== "" && fields.user.trim() !== "";
  const source = mode === "uri" ? uri : fieldsComplete ? buildUri(fields, sslMode) : "";
  const analysis = source.trim() === "" ? null : analyze(source, sslMode);
  // The TLS selector matters whenever the string does not carry its own mode.
  const needsSslChoice = mode === "fields" || (uri.trim() !== "" && !/[?&]sslmode=/i.test(uri));

  function chooseProvider(next: Provider) {
    const nextInfo = providerInfo(next);
    setProvider(next);
    setSslMode(nextInfo.defaultSsl);
    if (uri.trim() === "" && !fieldsComplete) setMode(nextInfo.defaultMode);
  }

  function onUriChange(value: string) {
    setUri(value);
    create.reset();
    // The host decides the platform: the picker follows whatever was pasted,
    // so the guidance shown always matches what will be registered.
    const host = HOST_IN_URI.exec(value)?.[1];
    if (!host) return;
    setProvider(detectProvider(host));
    if (isLocalHost(host) && sslMode === "require") setSslMode("prefer");
  }

  function onFix(patch: HostPatch) {
    if (mode === "uri") setUri(applyPatch(uri, patch));
    else setFields((f) => ({ ...f, host: patch.host ?? f.host, port: patch.port ?? f.port }));
  }

  const setField = (k: keyof ConnectionFields) => (value: string) => {
    setFields((f) => ({ ...f, [k]: value }));
    if (k === "host" && value.trim() !== "") setProvider(detectProvider(value.trim()));
    create.reset();
  };

  const portOk = /^\d*$/.test(fields.port.trim()) && (fields.port.trim() === "" || Number(fields.port) <= 65535);
  const errors = {
    name: name.trim() === "" ? "Give this database a display name." : null,
    uri: mode === "uri" && uri.trim() === "" ? "Paste the connection string." : null,
    host: mode === "fields" && fields.host.trim() === "" ? "Enter the host." : null,
    port: mode === "fields" && !portOk ? "Port must be 1–65535." : null,
    database: mode === "fields" && fields.database.trim() === "" ? "Enter the database name." : null,
    user: mode === "fields" && fields.user.trim() === "" ? "Enter the role." : null,
  };
  const visible = shown(errors, submitted);
  const blocked = Object.values(errors).some(Boolean) || !analysis || analysis.blocked;

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitted(true);
    if (blocked || !analysis) return;
    setRegistered(null);
    create.mutate({
      name: name.trim(),
      // Derived from the host, never from the picker alone.
      platform: analysis.platform,
      connectionUri: analysis.normalized,
    });
  }

  return (
    <form className="subpanel form" onSubmit={onSubmit} noValidate aria-label="Register a database">
      <div className="subpanel-head">
        <h3>Register a database</h3>
        <p className="muted">
          The connection is tested before it is saved. The string is stored encrypted and never shown again.
        </p>
      </div>

      <div className="providers" role="radiogroup" aria-label="Where is the database hosted?">
        {PROVIDERS.map(({ id, label, tagline, icon: Icon }) => (
          <label className="provider" key={id}>
            <input type="radio" name="provider" checked={provider === id} onChange={() => chooseProvider(id)} />
            <Icon size={16} aria-hidden />
            <span className="provider-text">
              <span className="provider-label">{label}</span>
              <span className="provider-tagline">{tagline}</span>
            </span>
          </label>
        ))}
      </div>

      <div className="register-grid">
        <div className="form">
          <Field label="Display name" htmlFor="db-name" error={visible.name}>
            <input
              {...control("db-name", visible.name)}
              value={name}
              maxLength={100}
              placeholder="production"
              autoFocus
              onChange={(e) => setName(e.target.value)}
            />
          </Field>

          <div className="segmented" role="tablist" aria-label="How to enter the connection">
            <button type="button" role="tab" aria-selected={mode === "uri"} onClick={() => setMode("uri")}>
              Paste connection string
            </button>
            <button type="button" role="tab" aria-selected={mode === "fields"} onClick={() => setMode("fields")}>
              Enter details
            </button>
          </div>

          {mode === "uri" ? (
            <Field label="postgres:// connection string" htmlFor="db-uri" error={visible.uri}>
              <div className="input-affix">
                <input
                  {...control("db-uri", visible.uri)}
                  type={reveal ? "text" : "password"}
                  className="mono"
                  value={uri}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder={info.example}
                  onChange={(e) => onUriChange(e.target.value)}
                />
                <Button
                  variant="ghost"
                  size="sm"
                  icon={reveal ? EyeOff : Eye}
                  tip={reveal ? "Hide" : "Show"}
                  className="tip-end"
                  onClick={() => setReveal(!reveal)}
                />
              </div>
            </Field>
          ) : (
            <div className="form-grid form-grid-conn">
              <Field label="Host" htmlFor="db-host" error={visible.host}>
                <input
                  {...control("db-host", visible.host)}
                  className="mono"
                  value={fields.host}
                  spellCheck={false}
                  placeholder="db.internal"
                  onChange={(e) => setField("host")(e.target.value)}
                />
              </Field>
              <Field label="Port" htmlFor="db-port" error={visible.port}>
                <input
                  {...control("db-port", visible.port)}
                  className="num"
                  inputMode="numeric"
                  value={fields.port}
                  placeholder="5432"
                  onChange={(e) => setField("port")(e.target.value)}
                />
              </Field>
              <Field label="Database" htmlFor="db-database" error={visible.database}>
                <input
                  {...control("db-database", visible.database)}
                  className="mono"
                  value={fields.database}
                  spellCheck={false}
                  placeholder="postgres"
                  onChange={(e) => setField("database")(e.target.value)}
                />
              </Field>
              <Field label="Role" htmlFor="db-user" error={visible.user}>
                <input
                  {...control("db-user", visible.user)}
                  className="mono"
                  value={fields.user}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder="backup"
                  onChange={(e) => setField("user")(e.target.value)}
                />
              </Field>
              <Field label="Password" htmlFor="db-password" hint="Special characters are encoded for you.">
                <input
                  id="db-password"
                  type="password"
                  className="mono"
                  value={fields.password}
                  autoComplete="new-password"
                  onChange={(e) => setField("password")(e.target.value)}
                />
              </Field>
            </div>
          )}

          {needsSslChoice && (
            <Field
              label="TLS mode (sslmode)"
              htmlFor="db-ssl"
              hint="require encrypts; verify-full also checks the server certificate against its hostname."
            >
              <select id="db-ssl" value={sslMode} onChange={(e) => setSslMode(e.target.value as SslMode)}>
                {SSL_MODES.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </Field>
          )}
        </div>

        <aside className="guide" aria-label={`${info.label} instructions`} key={provider}>
          <h4>
            <info.icon size={14} aria-hidden />
            Where to find it — {info.label}
          </h4>
          <ol>
            {info.steps.map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ol>
        </aside>
      </div>

      {analysis && <ConnectionPreflight analysis={analysis} onFix={onFix} />}

      {registered && (
        <InlineMessage tone="ok">
          Registered “{registered.name}”
          {registered.serverVersion && ` — PostgreSQL ${registered.serverVersion}`}.
        </InlineMessage>
      )}
      {registered?.poolingWarning && (
        <InlineMessage tone="warn" banner>
          {registered.poolingWarning}
        </InlineMessage>
      )}
      {create.isError && <InlineMessage>{errorMessage(create.error)}</InlineMessage>}

      <div className="form-actions">
        <Button type="submit" variant="primary" icon={PlugZap} loading={create.isPending}>
          {create.isPending ? "Testing connection…" : "Test & register"}
        </Button>
        <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
          {registered ? "Done" : "Cancel"}
        </Button>
      </div>
    </form>
  );
}
