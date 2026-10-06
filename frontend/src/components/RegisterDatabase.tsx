import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Eye, EyeOff, PlugZap } from "lucide-react";
import { api, type Database, type DatabaseCreate } from "../api/client";
import { useI18n } from "../i18n";
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

const EMPTY_FIELDS: ConnectionFields = {
  host: "",
  port: "5432",
  database: "",
  user: "",
  password: "",
};
const HOST_IN_URI = /@([^/?#]+?)(?::\d+)?(?:[/?#]|$)/;

// Database registration, guided per platform. The connection string is
// checked and prepared client-side (lib/connection.ts) so pooled endpoints,
// missing TLS modes and unsupported parameters are caught before the request;
// the server's own poolingWarning is still surfaced afterwards (P2-01).
export function RegisterDatabase({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const { t } = useI18n();
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
  const infoLabel = info.labelKey ? t(info.labelKey) : (info.label ?? "");
  const fieldsComplete =
    fields.host.trim() !== "" &&
    fields.database.trim() !== "" &&
    fields.user.trim() !== "";
  const source =
    mode === "uri" ? uri : fieldsComplete ? buildUri(fields, sslMode) : "";
  const analysis = source.trim() === "" ? null : analyze(source, sslMode);
  // The TLS selector matters whenever the string does not carry its own mode.
  const needsSslChoice =
    mode === "fields" || (uri.trim() !== "" && !/[?&]sslmode=/i.test(uri));

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
    else
      setFields((f) => ({
        ...f,
        host: patch.host ?? f.host,
        port: patch.port ?? f.port,
      }));
  }

  const setField = (k: keyof ConnectionFields) => (value: string) => {
    setFields((f) => ({ ...f, [k]: value }));
    if (k === "host" && value.trim() !== "")
      setProvider(detectProvider(value.trim()));
    create.reset();
  };

  const portOk =
    /^\d*$/.test(fields.port.trim()) &&
    (fields.port.trim() === "" || Number(fields.port) <= 65535);
  const errors = {
    name: name.trim() === "" ? t("reg.error.name") : null,
    uri: mode === "uri" && uri.trim() === "" ? t("reg.error.uri") : null,
    host:
      mode === "fields" && fields.host.trim() === ""
        ? t("reg.error.host")
        : null,
    port: mode === "fields" && !portOk ? t("reg.error.port") : null,
    database:
      mode === "fields" && fields.database.trim() === ""
        ? t("reg.error.database")
        : null,
    user:
      mode === "fields" && fields.user.trim() === ""
        ? t("reg.error.user")
        : null,
  };
  const visible = shown(errors, submitted);
  const blocked =
    Object.values(errors).some(Boolean) || !analysis || analysis.blocked;

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
    <form
      className="subpanel form"
      onSubmit={onSubmit}
      noValidate
      aria-label={t("reg.aria")}
    >
      <div className="subpanel-head">
        <h3>{t("reg.title")}</h3>
        <p className="muted">{t("reg.desc")}</p>
      </div>

      <div
        className="providers"
        role="radiogroup"
        aria-label={t("reg.providerAria")}
      >
        {PROVIDERS.map(({ id, label, labelKey, taglineKey, icon: Icon }) => (
          <label className="provider" key={id}>
            <input
              type="radio"
              name="provider"
              checked={provider === id}
              onChange={() => chooseProvider(id)}
            />
            <Icon size={16} aria-hidden />
            <span className="provider-text">
              <span className="provider-label">
                {labelKey ? t(labelKey) : label}
              </span>
              <span className="provider-tagline">{t(taglineKey)}</span>
            </span>
          </label>
        ))}
      </div>

      <div className="register-grid">
        <div className="form">
          <Field
            label={t("reg.field.name")}
            htmlFor="db-name"
            error={visible.name}
          >
            <input
              {...control("db-name", visible.name)}
              value={name}
              maxLength={100}
              placeholder="production"
              autoFocus
              onChange={(e) => setName(e.target.value)}
            />
          </Field>

          <div
            className="segmented"
            role="tablist"
            aria-label={t("reg.tab.aria")}
          >
            <button
              type="button"
              role="tab"
              aria-selected={mode === "uri"}
              onClick={() => setMode("uri")}
            >
              {t("reg.tab.uri")}
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={mode === "fields"}
              onClick={() => setMode("fields")}
            >
              {t("reg.tab.fields")}
            </button>
          </div>

          {mode === "uri" ? (
            <Field
              label={t("reg.field.uri")}
              htmlFor="db-uri"
              error={visible.uri}
            >
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
                  tip={reveal ? t("common.hide") : t("common.show")}
                  className="tip-end"
                  onClick={() => setReveal(!reveal)}
                />
              </div>
            </Field>
          ) : (
            <div className="form-grid form-grid-conn">
              <Field
                label={t("reg.field.host")}
                htmlFor="db-host"
                error={visible.host}
              >
                <input
                  {...control("db-host", visible.host)}
                  className="mono"
                  value={fields.host}
                  spellCheck={false}
                  placeholder="db.internal"
                  onChange={(e) => setField("host")(e.target.value)}
                />
              </Field>
              <Field
                label={t("reg.field.port")}
                htmlFor="db-port"
                error={visible.port}
              >
                <input
                  {...control("db-port", visible.port)}
                  className="num"
                  inputMode="numeric"
                  value={fields.port}
                  placeholder="5432"
                  onChange={(e) => setField("port")(e.target.value)}
                />
              </Field>
              <Field
                label={t("reg.field.database")}
                htmlFor="db-database"
                error={visible.database}
              >
                <input
                  {...control("db-database", visible.database)}
                  className="mono"
                  value={fields.database}
                  spellCheck={false}
                  placeholder="postgres"
                  onChange={(e) => setField("database")(e.target.value)}
                />
              </Field>
              <Field
                label={t("reg.field.user")}
                htmlFor="db-user"
                error={visible.user}
              >
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
              <Field
                label={t("reg.field.password")}
                htmlFor="db-password"
                hint={t("reg.hint.password")}
              >
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
              label={t("reg.field.ssl")}
              htmlFor="db-ssl"
              hint={t("reg.hint.ssl")}
            >
              <select
                id="db-ssl"
                value={sslMode}
                onChange={(e) => setSslMode(e.target.value as SslMode)}
              >
                {SSL_MODES.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </Field>
          )}
        </div>

        <aside
          className="guide"
          aria-label={t("reg.guide.aria", { label: infoLabel })}
          key={provider}
        >
          <h4>
            <info.icon size={14} aria-hidden />
            {t("reg.guide.title", { label: infoLabel })}
          </h4>
          <ol>
            {info.stepKeys.map((key) => (
              <li key={key}>{t(key)}</li>
            ))}
          </ol>
        </aside>
      </div>

      {analysis && <ConnectionPreflight analysis={analysis} onFix={onFix} />}

      {registered && (
        <InlineMessage tone="ok">
          {registered.serverVersion
            ? t("reg.registeredWithVersion", {
                name: registered.name,
                version: registered.serverVersion,
              })
            : t("reg.registered", { name: registered.name })}
        </InlineMessage>
      )}
      {registered?.poolingWarning && (
        <InlineMessage tone="warn" banner>
          {registered.poolingWarning}
        </InlineMessage>
      )}
      {create.isError && (
        <InlineMessage>{errorMessage(create.error)}</InlineMessage>
      )}

      <div className="form-actions">
        <Button
          type="submit"
          variant="primary"
          icon={PlugZap}
          loading={create.isPending}
        >
          {create.isPending ? t("reg.testing") : t("reg.test")}
        </Button>
        <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
          {registered ? t("common.done") : t("common.cancel")}
        </Button>
      </div>
    </form>
  );
}
