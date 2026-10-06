import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Pause, Play, Trash2 } from "lucide-react";
import { api, type OverviewEntry, type Task } from "../api/client";
import { useI18n } from "../i18n";
import { errorMessage } from "../lib/format";
import {
  lastSuccessUnix,
  protectionMeta,
  rowTone,
  taskStatusMeta,
  verifyMeta,
} from "../lib/status";
import { useToast } from "../lib/toast";
import { RunPulse } from "./RunPulse";
import { ScheduleForm } from "./ScheduleForm";
import { Badge, StatusBadge } from "./ui/Badge";
import { Beacon } from "./ui/Beacon";
import { Button } from "./ui/Button";
import { ConfirmButton } from "./ui/ConfirmButton";
import { InlineMessage } from "./ui/InlineMessage";
import { RelativeTime } from "./ui/RelativeTime";
import { Spinner } from "./ui/Spinner";

interface Props {
  entry: OverviewEntry;
  /** This database's tasks, newest first. */
  tasks: Task[];
  scheduleOpen: boolean;
  onToggleSchedule: () => void;
  onCloseSchedule: () => void;
}

export function DatabaseRow({
  entry,
  tasks,
  scheduleOpen,
  onToggleSchedule,
  onCloseSchedule,
}: Props) {
  const qc = useQueryClient();
  const toast = useToast();
  const { t } = useI18n();

  const backup = useMutation({
    mutationFn: () => api.backupNow(entry.databaseId),
    onSuccess: () => {
      toast("ok", t("dbrow.toast.queued", { name: entry.name }));
      qc.invalidateQueries({ queryKey: ["overview"] });
      qc.invalidateQueries({ queryKey: ["tasks"] });
    },
  });
  const del = useMutation({
    mutationFn: () => api.deleteDatabase(entry.databaseId),
    onSuccess: () => {
      toast("ok", t("dbrow.toast.removed", { name: entry.name }));
      qc.invalidateQueries();
    },
  });

  const state = protectionMeta(entry.state);
  const verify = verifyMeta(entry.lastSuccessVerifyStatus);
  const inFlight =
    entry.lastJobStatus === "running" || entry.lastJobStatus === "pending";
  const stripe = rowTone(entry);
  const actionError = backup.error ?? del.error;
  const lastSuccessAt = lastSuccessUnix(entry);

  return (
    <div
      className={`trow-group tone-${stripe}${scheduleOpen ? " trow-open" : ""}`}
      role="rowgroup"
    >
      <div className="trow" role="row">
        <div className="cell cell-main" role="cell">
          <Beacon
            tone={inFlight ? "signal" : stripe}
            live={inFlight || stripe === "ok" || stripe === "danger"}
          />
          <span className="db-name truncate">{entry.name}</span>
          <span className="chip">{entry.platform}</span>
          {entry.schedulePaused && (
            <Badge tone="neutral" icon={Pause}>
              {t("dbrow.paused")}
            </Badge>
          )}
        </div>
        <div className="cell" role="cell">
          <StatusBadge meta={state} />
        </div>
        <div className="cell" role="cell">
          {verify ? (
            <StatusBadge meta={verify} />
          ) : (
            <span className="muted">—</span>
          )}
        </div>
        <div className="cell" role="cell">
          <RunPulse tasks={tasks} />
        </div>
        <div className="cell cell-stack" role="cell">
          {entry.state === "never" || lastSuccessAt == null ? (
            <span className="muted">{t("dbrow.noSuccess")}</span>
          ) : (
            <span>
              <RelativeTime at={lastSuccessAt} />
              {entry.maxAgeHours ? (
                <span className="muted cell-limit">
                  {t("dbrow.limit", { n: entry.maxAgeHours })}
                </span>
              ) : null}
            </span>
          )}
          {entry.lastJobStatus === "failed" && (
            <span className="text-danger cell-note">
              {t("dbrow.lastJobFailed")}
            </span>
          )}
          {inFlight && (
            <span className="muted cell-note">
              <Spinner size={11} />{" "}
              {t("dbrow.backupState", {
                state: entry.lastJobStatus
                  ? t(taskStatusMeta(entry.lastJobStatus).label)
                  : "",
              })}
            </span>
          )}
        </div>
        <div className="cell cell-actions tip-end" role="cell">
          <Button
            size="sm"
            icon={Play}
            loading={backup.isPending}
            onClick={() => backup.mutate()}
          >
            {backup.isPending ? t("dbrow.queueing") : t("dbrow.backupNow")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            icon={CalendarClock}
            aria-expanded={scheduleOpen}
            tip={t("dbrow.scheduleTip")}
            onClick={onToggleSchedule}
          />
          <ConfirmButton
            icon={Trash2}
            tip={t("dbrow.removeTip")}
            prompt={t("dbrow.removePrompt", { name: entry.name })}
            confirmLabel={t("common.remove")}
            pending={del.isPending}
            onConfirm={() => del.mutate()}
          />
        </div>
      </div>
      {actionError && (
        <div className="trow-extra">
          <InlineMessage>{errorMessage(actionError)}</InlineMessage>
        </div>
      )}
      {scheduleOpen && (
        <ScheduleForm
          databaseId={entry.databaseId}
          name={entry.name}
          onDone={onCloseSchedule}
        />
      )}
    </div>
  );
}
