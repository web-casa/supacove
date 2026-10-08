import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import {
  FolderSync,
  HardDriveUpload,
  PlugZap,
  Plus,
  Trash2,
} from "lucide-react";
import {
  api,
  type Destination,
  type DestinationCreate,
  type ReconcileReport,
} from "../api/client";
import { useI18n } from "../i18n";
import { control, hasErrors, isHttpUrl, shown } from "../lib/form";
import { errorMessage } from "../lib/format";
import { databasesQuery, destinationsQuery } from "../lib/queries";
import { useToast } from "../lib/toast";
import { Button } from "./ui/Button";
import { ConfirmButton } from "./ui/ConfirmButton";
import { EmptyState } from "./ui/EmptyState";
import { Field } from "./ui/Field";
import { InlineMessage } from "./ui/InlineMessage";
import { Panel } from "./ui/Panel";
import { SkeletonRows } from "./ui/Skeleton";

type Platform = NonNullable<DestinationCreate["platform"]>;
const PLATFORMS: Platform[] = ["s3", "r2", "b2"];
const BUCKET_RE = /^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/;
const PREFIX_RE = /^[A-Za-z0-9/_.-]*$/;

function Reconciled({ report }: { report: ReconcileReport }) {
  const { t } = useI18n();
  const drift =
    (report.orphaned?.length ?? 0) +
    (report.missing?.length ?? 0) +
    (report.uncommitted?.length ?? 0);
  return (
    <InlineMessage tone={drift === 0 ? "ok" : "warn"}>
      {t("dest.reconcile.result", {
        remote: report.remoteObjects,
        matched: report.matched,
        orphaned: report.orphaned?.length ?? 0,
        missing: report.missing?.length ?? 0,
        uncommitted: report.uncommitted?.length ?? 0,
      })}
    </InlineMessage>
  );
}

function DestinationRow({
  dest,
  usedBy,
}: {
  dest: Destination;
  /** Names of the databases that upload here. */
  usedBy: string[];
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const { t } = useI18n();
  const test = useMutation({
    mutationFn: () => api.testDestination(dest.id),
  });
  const reconcile = useMutation({
    mutationFn: () => api.reconcileDestination(dest.id),
  });
  const del = useMutation({
    mutationFn: () => api.deleteDestination(dest.id),
    onSuccess: () => {
      toast("ok", t("dest.toast.removed", { name: dest.name }));
      qc.invalidateQueries({ queryKey: ["destinations"] });
    },
  });
  const location = `${dest.bucket}/${dest.prefix ?? ""}`;
  const feedback =
    test.data || test.error || reconcile.data || reconcile.error || del.error;

  return (
    <div className="trow-group" role="rowgroup">
      <div className="trow" role="row">
        <div className="cell cell-main" role="cell">
          <span className="db-name truncate">{dest.name}</span>
          <span className="chip">{dest.platform}</span>
        </div>
        <div className="cell cell-stack" role="cell">
          <span className="mono truncate" title={location}>
            {location}
          </span>
          {dest.endpoint && (
            <span
              className="mono muted truncate cell-note"
              title={dest.endpoint}
            >
              {dest.endpoint}
            </span>
          )}
        </div>
        <div className="cell" role="cell">
          <span className="muted">
            {dest.keepDays > 0
              ? t("dest.keep.both", { n: dest.keepRemote, days: dest.keepDays })
              : t("dest.keep.copies", { n: dest.keepRemote })}
          </span>
        </div>
        <div className="cell cell-badges" role="cell">
          {usedBy.length === 0 ? (
            <span className="muted">{t("dest.unused")}</span>
          ) : (
            usedBy.map((name) => (
              <span className="chip" key={name}>
                {name}
              </span>
            ))
          )}
        </div>
        <div className="cell cell-actions tip-end" role="cell">
          <Button
            size="sm"
            icon={PlugZap}
            loading={test.isPending}
            onClick={() => {
              reconcile.reset();
              test.mutate();
            }}
          >
            {t("dest.test")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            icon={FolderSync}
            tip={t("dest.reconcileTip")}
            loading={reconcile.isPending}
            onClick={() => {
              test.reset();
              reconcile.mutate();
            }}
          />
          <ConfirmButton
            icon={Trash2}
            tip={t("dest.removeTip")}
            prompt={t("dest.removePrompt", { name: dest.name })}
            confirmLabel={t("common.remove")}
            pending={del.isPending}
            onConfirm={() => del.mutate()}
          />
        </div>
      </div>
      {feedback && (
        <div className="trow-extra">
          {test.data && (
            <InlineMessage tone="ok">{t("dest.test.ok")}</InlineMessage>
          )}
          {test.error && (
            <InlineMessage>
              {t("dest.test.failed", { msg: errorMessage(test.error) })}
            </InlineMessage>
          )}
          {reconcile.data && <Reconciled report={reconcile.data} />}
          {reconcile.error && (
            <InlineMessage>{errorMessage(reconcile.error)}</InlineMessage>
          )}
          {del.error && (
            <InlineMessage>{errorMessage(del.error)}</InlineMessage>
          )}
        </div>
      )}
    </div>
  );
}

function AddDestination({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const { t } = useI18n();
  const [name, setName] = useState("");
  const [platform, setPlatform] = useState<Platform>("s3");
  const [endpoint, setEndpoint] = useState("");
  const [region, setRegion] = useState("");
  const [bucket, setBucket] = useState("");
  const [prefix, setPrefix] = useState("");
  const [accessKey, setAccessKey] = useState("");
  const [secretKey, setSecretKey] = useState("");
  const [keepRemote, setKeepRemote] = useState("10");
  const [keepDays, setKeepDays] = useState("0");
  const [submitted, setSubmitted] = useState(false);

  const create = useMutation({
    mutationFn: (body: DestinationCreate) => api.createDestination(body),
    onSuccess: (dest) => {
      toast("ok", t("dest.toast.added", { name: dest.name }));
      qc.invalidateQueries({ queryKey: ["destinations"] });
      onClose();
    },
  });

  const needsEndpoint = platform !== "s3";
  const ep = endpoint.trim();
  const copies = Number(keepRemote);
  const days = Number(keepDays);
  const errors = {
    name: name.trim() === "" ? t("dest.error.name") : null,
    endpoint:
      ep === ""
        ? needsEndpoint
          ? t("dest.error.endpointRequired")
          : null
        : isHttpUrl(ep) && new URL(ep).pathname === "/"
          ? null
          : t("dest.error.endpoint"),
    region:
      platform === "b2" && region.trim() === "" ? t("dest.error.region") : null,
    bucket: BUCKET_RE.test(bucket.trim()) ? null : t("dest.error.bucket"),
    prefix:
      PREFIX_RE.test(prefix.trim()) && !prefix.includes("..")
        ? null
        : t("dest.error.prefix"),
    accessKey: accessKey.trim().length < 3 ? t("dest.error.accessKey") : null,
    secretKey: secretKey.length < 8 ? t("dest.error.secretKey") : null,
    keepRemote:
      Number.isInteger(copies) && copies >= 1
        ? null
        : t("dest.error.keepRemote"),
    keepDays:
      Number.isInteger(days) && days >= 0 ? null : t("dest.error.keepDays"),
  };
  const visible = shown(errors, submitted);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitted(true);
    if (hasErrors(errors)) return;
    create.mutate({
      name: name.trim(),
      platform,
      endpoint: ep || undefined,
      region: region.trim() || undefined,
      bucket: bucket.trim(),
      prefix: prefix.trim() || undefined,
      accessKey: accessKey.trim(),
      secretKey,
      verifyReadback: true,
      keepRemote: copies,
      keepDays: days,
    });
  }

  return (
    <form
      className="subpanel form"
      onSubmit={onSubmit}
      noValidate
      aria-label={t("dest.addForm.aria")}
    >
      <div className="subpanel-head">
        <h3>{t("dest.add")}</h3>
        <p className="muted">{t("dest.addForm.desc")}</p>
      </div>
      <div className="form-grid form-grid-3">
        <Field
          label={t("dest.field.name")}
          htmlFor="dest-name"
          error={visible.name}
        >
          <input
            {...control("dest-name", visible.name)}
            value={name}
            maxLength={100}
            placeholder="offsite-s3"
            autoFocus
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label={t("dest.field.platform")} htmlFor="dest-platform">
          <select
            id="dest-platform"
            value={platform}
            onChange={(e) => setPlatform(e.target.value as Platform)}
          >
            {PLATFORMS.map((p) => (
              <option key={p} value={p}>
                {t(`dest.platform.${p}`)}
              </option>
            ))}
          </select>
        </Field>
        <Field
          label={t("dest.field.region")}
          htmlFor="dest-region"
          hint={t(`dest.hint.region.${platform}`)}
          error={visible.region}
        >
          <input
            {...control("dest-region", visible.region)}
            className="mono"
            value={region}
            autoComplete="off"
            onChange={(e) => setRegion(e.target.value)}
          />
        </Field>
      </div>
      <Field
        label={t("dest.field.endpoint")}
        htmlFor="dest-endpoint"
        hint={t(needsEndpoint ? "dest.hint.endpoint" : "dest.hint.endpointS3")}
        error={visible.endpoint}
      >
        <input
          {...control("dest-endpoint", visible.endpoint)}
          className="mono"
          value={endpoint}
          autoComplete="off"
          placeholder="https://"
          onChange={(e) => setEndpoint(e.target.value)}
        />
      </Field>
      <div className="form-grid form-grid-register">
        <Field
          label={t("dest.field.bucket")}
          htmlFor="dest-bucket"
          hint={t("dest.hint.bucket")}
          error={visible.bucket}
        >
          <input
            {...control("dest-bucket", visible.bucket)}
            className="mono"
            value={bucket}
            autoComplete="off"
            onChange={(e) => setBucket(e.target.value)}
          />
        </Field>
        <Field
          label={t("dest.field.prefix")}
          htmlFor="dest-prefix"
          hint={t("dest.hint.prefix")}
          error={visible.prefix}
        >
          <input
            {...control("dest-prefix", visible.prefix)}
            className="mono"
            value={prefix}
            autoComplete="off"
            placeholder="supacove"
            onChange={(e) => setPrefix(e.target.value)}
          />
        </Field>
      </div>
      <div className="form-grid form-grid-register">
        <Field
          label={t("dest.field.accessKey")}
          htmlFor="dest-access"
          error={visible.accessKey}
        >
          <input
            {...control("dest-access", visible.accessKey)}
            className="mono"
            value={accessKey}
            autoComplete="off"
            onChange={(e) => setAccessKey(e.target.value)}
          />
        </Field>
        <Field
          label={t("dest.field.secretKey")}
          htmlFor="dest-secret"
          hint={t("dest.hint.secretKey")}
          error={visible.secretKey}
        >
          <input
            {...control("dest-secret", visible.secretKey)}
            type="password"
            className="mono"
            value={secretKey}
            autoComplete="new-password"
            onChange={(e) => setSecretKey(e.target.value)}
          />
        </Field>
      </div>
      <div className="form-grid form-grid-3">
        <Field
          label={t("dest.field.keepRemote")}
          htmlFor="dest-keep"
          hint={t("dest.hint.keepRemote")}
          error={visible.keepRemote}
        >
          <input
            {...control("dest-keep", visible.keepRemote)}
            type="number"
            min={1}
            value={keepRemote}
            onChange={(e) => setKeepRemote(e.target.value)}
          />
        </Field>
        <Field
          label={t("dest.field.keepDays")}
          htmlFor="dest-days"
          hint={t("dest.hint.keepDays")}
          error={visible.keepDays}
        >
          <input
            {...control("dest-days", visible.keepDays)}
            type="number"
            min={0}
            value={keepDays}
            onChange={(e) => setKeepDays(e.target.value)}
          />
        </Field>
      </div>

      {create.isError && (
        <InlineMessage>{errorMessage(create.error)}</InlineMessage>
      )}

      <div className="form-actions">
        <Button
          type="submit"
          variant="primary"
          icon={Plus}
          loading={create.isPending}
        >
          {create.isPending ? t("dest.testing") : t("dest.addSubmit")}
        </Button>
        <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
          {t("common.cancel")}
        </Button>
      </div>
    </form>
  );
}

// Storage destinations: list / add / test / reconcile / remove.
export function DestinationsPanel() {
  const { t } = useI18n();
  const dests = useQuery(destinationsQuery);
  const dbs = useQuery(databasesQuery);
  const [adding, setAdding] = useState(false);
  const list = dests.data?.destinations ?? [];
  const usersOf = (id: number) =>
    (dbs.data?.databases ?? [])
      .filter((d) => d.destinationId === id)
      .map((d) => d.name);

  return (
    <Panel
      title={t("dest.title")}
      description={t("dest.desc")}
      actions={
        !adding &&
        list.length > 0 && (
          <Button
            variant="primary"
            size="sm"
            icon={Plus}
            onClick={() => setAdding(true)}
          >
            {t("dest.add")}
          </Button>
        )
      }
    >
      {adding && <AddDestination onClose={() => setAdding(false)} />}
      {dests.isPending && <SkeletonRows rows={2} />}
      {dests.isError && !dests.data && (
        <InlineMessage>
          {t("dest.unavailable", { msg: errorMessage(dests.error) })}
        </InlineMessage>
      )}
      {dests.data && list.length === 0 && !adding && (
        <EmptyState
          icon={HardDriveUpload}
          title={t("dest.empty.title")}
          action={
            <Button
              variant="primary"
              icon={Plus}
              onClick={() => setAdding(true)}
            >
              {t("dest.empty.cta")}
            </Button>
          }
        >
          {t("dest.empty.body")}
        </EmptyState>
      )}
      {list.length > 0 && (
        <div
          className="table table-dests stagger"
          role="table"
          aria-label={t("dest.aria")}
        >
          <div className="thead" role="row">
            <span role="columnheader">{t("dest.col.name")}</span>
            <span role="columnheader">{t("dest.col.location")}</span>
            <span role="columnheader">{t("dest.col.retention")}</span>
            <span role="columnheader">{t("dest.col.usedBy")}</span>
            <span role="columnheader" className="sr-only">
              {t("table.col.actions")}
            </span>
          </div>
          {list.map((d) => (
            <DestinationRow key={d.id} dest={d} usedBy={usersOf(d.id)} />
          ))}
        </div>
      )}
    </Panel>
  );
}
