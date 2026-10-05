import type { Task } from "../api/client";
import { useI18n } from "../i18n";
import { relativeTime } from "../lib/format";
import { taskStatusMeta } from "../lib/status";

const SLOTS = 12;

/** The last runs of one database as ticks, oldest → newest. */
export function RunPulse({ tasks }: { tasks: Task[] }) {
  const { t } = useI18n();
  // The API lists newest first.
  const recent = tasks.slice(0, SLOTS).reverse();
  const empty = SLOTS - recent.length;
  const label =
    recent.length === 0
      ? t("pulse.noRuns")
      : t("pulse.lastRuns", { n: recent.length, list: recent.map((task) => t(taskStatusMeta(task.status).label)).join(", ") });

  return (
    <div className="pulse" role="img" aria-label={label}>
      {Array.from({ length: empty }, (_, i) => (
        <span className="tick tick-empty" key={`empty-${i}`} />
      ))}
      {recent.map((task) => {
        const meta = taskStatusMeta(task.status);
        const when = task.finishedAt ?? task.startedAt ?? task.scheduledAt;
        return (
          <span
            key={task.id}
            className={`tick tone-${meta.tone}${meta.spin ? " tick-live" : ""}`}
            data-tip={`#${task.id} ${t(meta.label)}${when ? ` · ${relativeTime(when)}` : ""}`}
          />
        );
      })}
    </div>
  );
}
