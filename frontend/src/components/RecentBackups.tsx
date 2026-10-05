import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Cloud, CloudUpload, Download, History, LifeBuoy, Wrench } from "lucide-react";
import { artifactDownloadPath, kitDownloadPath, presignedUrl, type Task } from "../api/client";
import { errorMessage, humanBytes } from "../lib/format";
import { overviewQuery, tasksQuery } from "../lib/queries";
import { taskStatusMeta, verifyMeta } from "../lib/status";
import { Badge, StatusBadge } from "./ui/Badge";
import { Button, LinkButton } from "./ui/Button";
import { EmptyState } from "./ui/EmptyState";
import { InlineMessage } from "./ui/InlineMessage";
import { Panel } from "./ui/Panel";
import { RelativeTime } from "./ui/RelativeTime";
import { SkeletonRows } from "./ui/Skeleton";

const PAGE = 10;

function TaskRow({ task: t, databaseName }: { task: Task; databaseName?: string }) {
  // The presigned URL is short-lived, so it is fetched on click, not up front.
  const bucket = useMutation({
    mutationFn: () => presignedUrl(t.id),
    onSuccess: (url) => window.open(url, "_blank", "noopener"),
  });
  // Download links carry `download`: the API sends no Content-Disposition, so
  // a bare link would navigate away and render the file inline.
  const status = taskStatusMeta(t.status);
  const verify = verifyMeta(t.verifyStatus);
  const committed = t.remoteState === "committed";
  const when = t.finishedAt ?? t.startedAt ?? t.scheduledAt;

  return (
    <div className={`trow-group tone-${status.tone}${t.status === "running" ? " trow-running" : ""}`} role="rowgroup">
      <div className="trow" role="row">
        <div className="cell cell-main" role="cell">
          <span className="num muted">#{t.id}</span>
          <span className="db-name truncate">{databaseName ?? `database ${t.databaseId}`}</span>
          {t.attempt > 1 && <span className="chip">attempt {t.attempt}</span>}
        </div>
        <div className="cell" role="cell">
          <StatusBadge meta={status} />
        </div>
        <div className="cell cell-badges" role="cell">
          {verify && <StatusBadge meta={verify} />}
          {committed && (
            <Badge tone="info" icon={Cloud}>
              remote
            </Badge>
          )}
          {t.remoteState === "uploading" && (
            <Badge tone="neutral" icon={CloudUpload}>
              uploading
            </Badge>
          )}
        </div>
        <div className="cell num cell-right" role="cell">
          {humanBytes(t.artifactSize)}
        </div>
        <div className="cell cell-right tip-end" role="cell">
          {when ? <RelativeTime at={when} /> : <span className="muted">—</span>}
        </div>
        <div className="cell cell-actions" role="cell">
          {t.status === "succeeded" && t.hasRecoveryKit && (
            <LinkButton variant="ghost" size="sm" icon={LifeBuoy} href={kitDownloadPath(t.id)} download={`restore-job${t.id}.sh`}>
              Recovery kit
            </LinkButton>
          )}
          {t.status === "succeeded" && committed && (
            <Button variant="ghost" size="sm" icon={Cloud} loading={bucket.isPending} onClick={() => bucket.mutate()}>
              Download (bucket)
            </Button>
          )}
          {t.status === "succeeded" && !committed && (
            <LinkButton variant="ghost" size="sm" icon={Download} href={artifactDownloadPath(t.id)} download={`backup-job${t.id}.dump.age`}>
              Download
            </LinkButton>
          )}
        </div>
      </div>
      {t.status === "failed" && (t.remediation || t.errorMessage) && (
        <div className="trow-extra remediation">
          <Wrench size={13} aria-hidden />
          <div>
            <span className="remediation-title">
              How to fix{t.errorClass ? <span className="chip">{t.errorClass}</span> : null}
            </span>
            {t.errorMessage && <p className="mono text-danger">{t.errorMessage}</p>}
            {t.remediation && <p>{t.remediation}</p>}
          </div>
        </div>
      )}
      {bucket.isError && (
        <div className="trow-extra">
          <InlineMessage>Could not get a download link: {errorMessage(bucket.error)}</InlineMessage>
        </div>
      )}
    </div>
  );
}

// Recent tasks with kit/artifact downloads (kit = Phase 5 recovery kit).
export function RecentBackups() {
  const tasks = useQuery(tasksQuery);
  const overview = useQuery(overviewQuery);
  const [showAll, setShowAll] = useState(false);
  const list = tasks.data?.tasks ?? [];
  const names = new Map(overview.data?.databases.map((d) => [d.databaseId, d.name]));
  const visible = showAll ? list : list.slice(0, PAGE);

  return (
    <Panel title="Recent backups" description="Newest first. Refreshes every 15s.">
      {tasks.isPending && <SkeletonRows rows={4} />}
      {tasks.isError && !tasks.data && <InlineMessage>Backups unavailable: {errorMessage(tasks.error)}</InlineMessage>}
      {tasks.data && list.length === 0 && (
        <EmptyState icon={History} title="No backups yet">
          Run “Back up now” on a database in the overview, or give it a cron schedule. Every run shows up here
          with its verification result and downloads.
        </EmptyState>
      )}
      {list.length > 0 && (
        <div className="table table-tasks stagger" role="table" aria-label="Backup tasks">
          <div className="thead" role="row">
            <span role="columnheader">Backup</span>
            <span role="columnheader">Status</span>
            <span role="columnheader">Checks</span>
            <span role="columnheader" className="cell-right">
              Size
            </span>
            <span role="columnheader" className="cell-right">
              When
            </span>
            <span role="columnheader" className="sr-only">
              Downloads
            </span>
          </div>
          {visible.map((t) => (
            <TaskRow key={t.id} task={t} databaseName={names.get(t.databaseId)} />
          ))}
        </div>
      )}
      {list.length > PAGE && (
        <div className="panel-foot">
          <Button variant="ghost" size="sm" onClick={() => setShowAll(!showAll)}>
            {showAll ? `Show latest ${PAGE}` : `Show all ${list.length}`}
          </Button>
        </div>
      )}
    </Panel>
  );
}
