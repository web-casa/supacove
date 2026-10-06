import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import {
  BellPlus,
  Plus,
  Send,
  Trash2,
  Webhook as WebhookIcon,
} from "lucide-react";
import {
  api,
  type Webhook,
  type WebhookCreate,
  type WebhookTest,
} from "../api/client";
import { useI18n } from "../i18n";
import { control, hasErrors, isHttpUrl, shown } from "../lib/form";
import { errorMessage } from "../lib/format";
import { webhooksQuery } from "../lib/queries";
import { useToast } from "../lib/toast";
import { Button } from "./ui/Button";
import { ConfirmButton } from "./ui/ConfirmButton";
import { EmptyState } from "./ui/EmptyState";
import { Field } from "./ui/Field";
import { InlineMessage } from "./ui/InlineMessage";
import { Panel } from "./ui/Panel";
import { SkeletonRows } from "./ui/Skeleton";

type EventType = WebhookCreate["events"][number];
const EVENT_TYPES: EventType[] = [
  "backup_failed",
  "backup_expired",
  "verification_failed",
];

/** Outcome of a test delivery, from either the response body or a thrown error. */
function TestResult({
  result,
  error,
}: {
  result?: WebhookTest;
  error: unknown;
}) {
  const { t } = useI18n();
  if (error)
    return (
      <InlineMessage>
        {t("wh.test.failed", { msg: errorMessage(error) })}
      </InlineMessage>
    );
  if (!result) return null;
  return result.delivered ? (
    <InlineMessage tone="ok">{t("wh.test.delivered")}</InlineMessage>
  ) : (
    <InlineMessage>
      {t("wh.test.failed", { msg: result.detail ?? t("wh.test.unknown") })}
    </InlineMessage>
  );
}

function WebhookRow({ hook }: { hook: Webhook }) {
  const qc = useQueryClient();
  const toast = useToast();
  const { t } = useI18n();
  const test = useMutation({
    mutationFn: () =>
      api.testWebhook({
        name: hook.name,
        url: hook.url,
        events: [...(hook.events ?? [])],
      }),
  });
  const del = useMutation({
    mutationFn: () => api.deleteWebhook(hook.id),
    onSuccess: () => {
      toast("ok", t("wh.toast.removed", { name: hook.name }));
      qc.invalidateQueries({ queryKey: ["webhooks"] });
    },
  });

  return (
    <div className="trow-group" role="rowgroup">
      <div className="trow" role="row">
        <div className="cell cell-main" role="cell">
          <span className="db-name truncate">{hook.name}</span>
        </div>
        <div className="cell cell-badges" role="cell">
          {(hook.events ?? []).map((ev) => (
            <span className="chip mono" key={ev}>
              {ev}
            </span>
          ))}
        </div>
        <div className="cell" role="cell">
          <span className="mono muted truncate" title={hook.url}>
            {hook.url}
          </span>
        </div>
        <div className="cell cell-actions tip-end" role="cell">
          <Button
            size="sm"
            icon={Send}
            loading={test.isPending}
            onClick={() => test.mutate()}
          >
            {t("wh.sendTest")}
          </Button>
          <ConfirmButton
            icon={Trash2}
            tip={t("wh.removeTip")}
            prompt={t("wh.removePrompt", { name: hook.name })}
            confirmLabel={t("common.remove")}
            pending={del.isPending}
            onConfirm={() => del.mutate()}
          />
        </div>
      </div>
      {(test.data || test.error || del.error) && (
        <div className="trow-extra">
          <TestResult result={test.data} error={test.error} />
          {del.error && (
            <InlineMessage>{errorMessage(del.error)}</InlineMessage>
          )}
        </div>
      )}
    </div>
  );
}

function AddWebhook({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const { t } = useI18n();
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [events, setEvents] = useState<EventType[]>([
    "backup_failed",
    "backup_expired",
  ]);
  const [submitted, setSubmitted] = useState(false);

  const create = useMutation({
    mutationFn: (body: WebhookCreate) => api.createWebhook(body),
    onSuccess: (hook) => {
      toast("ok", t("wh.toast.added", { name: hook.name }));
      qc.invalidateQueries({ queryKey: ["webhooks"] });
      onClose();
    },
  });
  const test = useMutation({
    mutationFn: (body: WebhookCreate) => api.testWebhook(body),
  });

  const toggleEvent = (ev: EventType) =>
    setEvents((cur) =>
      cur.includes(ev) ? cur.filter((x) => x !== ev) : [...cur, ev],
    );

  const trimmedUrl = url.trim();
  const urlError =
    trimmedUrl === ""
      ? t("wh.error.url")
      : !isHttpUrl(trimmedUrl)
        ? t("wh.error.urlScheme")
        : null;
  const errors = {
    name: name.trim() === "" ? t("wh.error.name") : null,
    url: urlError,
    events: events.length === 0 ? t("wh.error.events") : null,
  };
  const visible = shown(errors, submitted);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitted(true);
    if (hasErrors(errors)) return;
    create.mutate({ name: name.trim(), url: trimmedUrl, events });
  }

  return (
    <form
      className="subpanel form"
      onSubmit={onSubmit}
      noValidate
      aria-label={t("wh.addForm.aria")}
    >
      <div className="subpanel-head">
        <h3>{t("wh.add")}</h3>
        <p className="muted">{t("wh.addForm.desc")}</p>
      </div>
      <div className="form-grid form-grid-register">
        <Field
          label={t("wh.field.name")}
          htmlFor="wh-name"
          error={visible.name}
        >
          <input
            {...control("wh-name", visible.name)}
            value={name}
            maxLength={100}
            placeholder="ops-alerts"
            autoFocus
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label={t("wh.field.url")} htmlFor="wh-url" error={visible.url}>
          <input
            {...control("wh-url", visible.url)}
            type="password"
            className="mono"
            value={url}
            autoComplete="off"
            onChange={(e) => {
              setUrl(e.target.value);
              test.reset();
            }}
          />
        </Field>
      </div>
      <fieldset className={`field${visible.events ? " field-invalid" : ""}`}>
        <legend>{t("wh.fieldset.events")}</legend>
        <div className="check-row">
          {EVENT_TYPES.map((ev) => (
            <label className="check" key={ev}>
              <input
                type="checkbox"
                checked={events.includes(ev)}
                onChange={() => toggleEvent(ev)}
              />
              <span className="mono">{ev}</span>
            </label>
          ))}
        </div>
        {visible.events && (
          <p className="field-msg field-error" role="alert">
            {visible.events}
          </p>
        )}
      </fieldset>

      {create.isError && (
        <InlineMessage>{errorMessage(create.error)}</InlineMessage>
      )}
      <TestResult result={test.data} error={test.error} />

      <div className="form-actions">
        <Button
          type="submit"
          variant="primary"
          icon={Plus}
          loading={create.isPending}
        >
          {t("wh.add")}
        </Button>
        <Button
          icon={Send}
          loading={test.isPending}
          disabled={urlError !== null}
          onClick={() =>
            test.mutate({
              name: name.trim() || "test",
              url: trimmedUrl,
              events,
            })
          }
        >
          {t("wh.testThisUrl")}
        </Button>
        <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
          {t("common.cancel")}
        </Button>
      </div>
    </form>
  );
}

// Webhooks: list / add / test / remove.
export function WebhooksPanel() {
  const { t } = useI18n();
  const hooks = useQuery(webhooksQuery);
  const [adding, setAdding] = useState(false);
  const list = hooks.data?.webhooks ?? [];

  return (
    <Panel
      title={t("wh.title")}
      description={t("wh.desc")}
      actions={
        !adding &&
        list.length > 0 && (
          <Button
            variant="primary"
            size="sm"
            icon={BellPlus}
            onClick={() => setAdding(true)}
          >
            {t("wh.add")}
          </Button>
        )
      }
    >
      {adding && <AddWebhook onClose={() => setAdding(false)} />}
      {hooks.isPending && <SkeletonRows rows={2} />}
      {hooks.isError && !hooks.data && (
        <InlineMessage>
          {t("wh.unavailable", { msg: errorMessage(hooks.error) })}
        </InlineMessage>
      )}
      {hooks.data && list.length === 0 && !adding && (
        <EmptyState
          icon={WebhookIcon}
          title={t("wh.empty.title")}
          action={
            <Button
              variant="primary"
              icon={BellPlus}
              onClick={() => setAdding(true)}
            >
              {t("wh.empty.cta")}
            </Button>
          }
        >
          {t("wh.empty.body")}
        </EmptyState>
      )}
      {list.length > 0 && (
        <div
          className="table table-hooks stagger"
          role="table"
          aria-label={t("wh.aria")}
        >
          <div className="thead" role="row">
            <span role="columnheader">{t("wh.col.name")}</span>
            <span role="columnheader">{t("wh.col.events")}</span>
            <span role="columnheader">{t("wh.col.url")}</span>
            <span role="columnheader" className="sr-only">
              {t("table.col.actions")}
            </span>
          </div>
          {list.map((w) => (
            <WebhookRow key={w.id} hook={w} />
          ))}
        </div>
      )}
    </Panel>
  );
}
