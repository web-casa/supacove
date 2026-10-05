import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, type ScheduleConfig } from "../api/client";
import { useI18n } from "../i18n";
import { control, hasErrors, isHttpUrl, shown } from "../lib/form";
import { errorMessage } from "../lib/format";
import { useToast } from "../lib/toast";
import { Button } from "./ui/Button";
import { Field } from "./ui/Field";
import { InlineMessage } from "./ui/InlineMessage";
import { RelativeTime } from "./ui/RelativeTime";
import { Spinner } from "./ui/Spinner";

const MAX_HOURS = 8760;
const TIMEZONES: string[] = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];

function validTimezone(tz: string): boolean {
  try {
    new Intl.DateTimeFormat(undefined, { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

interface Props {
  databaseId: number;
  name: string;
  onDone: () => void;
}

export function ScheduleForm({ databaseId, name, onDone }: Props) {
  const qc = useQueryClient();
  const toast = useToast();
  const { t } = useI18n();
  const existing = useQuery({
    queryKey: ["schedule", databaseId],
    queryFn: () => api.getSchedule(databaseId),
  });
  // Only the fields the user touched; everything else falls through to the
  // server's current values.
  const [form, setForm] = useState<Partial<ScheduleConfig>>({});
  const [submitted, setSubmitted] = useState(false);
  const cur = existing.data;

  const hoursError = (v: number): string | null => {
    if (!Number.isInteger(v) || v < 0) return t("schedule.error.hoursWhole");
    if (v > MAX_HOURS) return t("schedule.error.hoursMax", { n: MAX_HOURS });
    return null;
  };

  const v = {
    cronExpr: form.cronExpr ?? cur?.cronExpr ?? "",
    cronTz: form.cronTz ?? cur?.cronTz ?? "UTC",
    maxAgeHours: form.maxAgeHours ?? cur?.maxAgeHours ?? 0,
    paused: form.paused ?? cur?.paused ?? false,
    heartbeatUrl: form.heartbeatUrl ?? cur?.heartbeatUrl ?? "",
    heartbeatPeriodHours: form.heartbeatPeriodHours ?? cur?.heartbeatPeriodHours ?? 0,
    heartbeatGraceHours: form.heartbeatGraceHours ?? cur?.heartbeatGraceHours ?? 0,
  };
  const set = <K extends keyof ScheduleConfig>(k: K, value: ScheduleConfig[K]) =>
    setForm((f) => ({ ...f, [k]: value }));

  const save = useMutation({
    mutationFn: () =>
      api.putSchedule(databaseId, {
        ...v,
        cronExpr: v.cronExpr.trim(),
        cronTz: v.cronTz.trim(),
        heartbeatUrl: v.heartbeatUrl.trim(),
      }),
    onSuccess: (saved) => {
      qc.setQueryData(["schedule", databaseId], saved);
      qc.invalidateQueries({ queryKey: ["overview"] });
      toast("ok", t("schedule.toast.saved", { name }));
      onDone();
    },
  });

  const cron = v.cronExpr.trim();
  const hbUrl = v.heartbeatUrl.trim();
  const hbActive = hbUrl !== "" && hbUrl !== "-";
  const errors = {
    cronExpr:
      cron !== "" && !cron.startsWith("@") && cron.split(/\s+/).length !== 5
        ? t("schedule.error.cron")
        : null,
    cronTz: validTimezone(v.cronTz.trim()) ? null : t("schedule.error.tz"),
    maxAgeHours: hoursError(v.maxAgeHours),
    heartbeatUrl: hbActive && !isHttpUrl(hbUrl) ? t("schedule.error.hbUrl") : null,
    heartbeatPeriodHours:
      hoursError(v.heartbeatPeriodHours) ??
      (hbActive && v.heartbeatPeriodHours <= 0 ? t("schedule.error.period") : null),
    heartbeatGraceHours: hoursError(v.heartbeatGraceHours),
  };
  const visible = shown(errors, submitted);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitted(true);
    if (hasErrors(errors)) return;
    save.mutate();
  }

  const id = (field: string) => `${field}-${databaseId}`;

  if (existing.isPending) {
    return (
      <div className="subpanel">
        <p className="muted">
          <Spinner /> {t("schedule.loading")}
        </p>
      </div>
    );
  }
  if (existing.isError) {
    return (
      <div className="subpanel">
        <InlineMessage>{t("schedule.unavailable", { msg: errorMessage(existing.error) })}</InlineMessage>
        <div className="form-actions">
          <Button size="sm" onClick={() => existing.refetch()}>
            {t("common.retry")}
          </Button>
          <Button variant="ghost" size="sm" onClick={onDone}>
            {t("common.close")}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <form className="subpanel form" onSubmit={onSubmit} noValidate aria-label={t("schedule.aria", { name })}>
      <div className="subpanel-head">
        <h3>{t("schedule.title")}</h3>
        <p className="muted">
          {t("schedule.lastScheduled")} {cur?.lastScheduledAt ? <RelativeTime at={cur.lastScheduledAt} /> : t("common.never")} ·{" "}
          {t("schedule.lastHeartbeat")} {cur?.lastHeartbeatAt ? <RelativeTime at={cur.lastHeartbeatAt} /> : t("common.never")}
        </p>
      </div>

      <div className="form-grid form-grid-3">
        <Field label={t("schedule.field.cron")} htmlFor={id("cron")} error={visible.cronExpr} hint={t("schedule.hint.cron")}>
          <input
            {...control(id("cron"), visible.cronExpr)}
            className="mono"
            placeholder="0 3 * * *"
            value={v.cronExpr}
            spellCheck={false}
            onChange={(e) => set("cronExpr", e.target.value)}
          />
        </Field>
        <Field label={t("schedule.field.tz")} htmlFor={id("tz")} error={visible.cronTz}>
          <input
            {...control(id("tz"), visible.cronTz)}
            list={id("tz-list")}
            value={v.cronTz}
            spellCheck={false}
            onChange={(e) => set("cronTz", e.target.value)}
          />
          <datalist id={id("tz-list")}>
            {TIMEZONES.map((tz) => (
              <option key={tz} value={tz} />
            ))}
          </datalist>
        </Field>
        <Field
          label={t("schedule.field.maxAge")}
          htmlFor={id("maxage")}
          error={visible.maxAgeHours}
          hint={t("schedule.hint.maxAge")}
        >
          <input
            {...control(id("maxage"), visible.maxAgeHours)}
            className="num"
            type="number"
            min={0}
            max={MAX_HOURS}
            value={v.maxAgeHours}
            onChange={(e) => set("maxAgeHours", Number(e.target.value))}
          />
        </Field>
        <Field
          label={t("schedule.field.hbUrl")}
          htmlFor={id("hb")}
          error={visible.heartbeatUrl}
          hint={t("schedule.hint.hbUrl")}
        >
          <input
            {...control(id("hb"), visible.heartbeatUrl)}
            className="mono"
            placeholder="https://hc-ping.com/…"
            value={v.heartbeatUrl}
            spellCheck={false}
            onChange={(e) => set("heartbeatUrl", e.target.value)}
          />
        </Field>
        <Field label={t("schedule.field.period")} htmlFor={id("hbp")} error={visible.heartbeatPeriodHours}>
          <input
            {...control(id("hbp"), visible.heartbeatPeriodHours)}
            className="num"
            type="number"
            min={0}
            max={MAX_HOURS}
            value={v.heartbeatPeriodHours}
            onChange={(e) => set("heartbeatPeriodHours", Number(e.target.value))}
          />
        </Field>
        <Field label={t("schedule.field.grace")} htmlFor={id("hbg")} error={visible.heartbeatGraceHours}>
          <input
            {...control(id("hbg"), visible.heartbeatGraceHours)}
            className="num"
            type="number"
            min={0}
            max={MAX_HOURS}
            value={v.heartbeatGraceHours}
            onChange={(e) => set("heartbeatGraceHours", Number(e.target.value))}
          />
        </Field>
      </div>

      <label className="switch">
        <input type="checkbox" role="switch" checked={v.paused} onChange={(e) => set("paused", e.target.checked)} />
        <span className="switch-track" aria-hidden />
        <span>
          {t("schedule.pause")}
          <span className="muted switch-note">{t("schedule.pauseNote")}</span>
        </span>
      </label>

      {save.isError && <InlineMessage>{errorMessage(save.error)}</InlineMessage>}
      <div className="form-actions">
        <Button type="submit" variant="primary" loading={save.isPending}>
          {save.isPending ? t("common.saving") : t("common.save")}
        </Button>
        <Button variant="ghost" onClick={onDone} disabled={save.isPending}>
          {t("common.cancel")}
        </Button>
      </div>
    </form>
  );
}
