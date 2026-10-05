import type { Task } from "../api/client";
import { relativeTime } from "../lib/format";
import { taskStatusMeta } from "../lib/status";

const SLOTS = 12;

/** The last runs of one database as ticks, oldest → newest. */
export function RunPulse({ tasks }: { tasks: Task[] }) {
  // The API lists newest first.
  const recent = tasks.slice(0, SLOTS).reverse();
  const empty = SLOTS - recent.length;
  const label =
    recent.length === 0 ? "No runs yet" : `Last ${recent.length} runs: ${recent.map((t) => t.status).join(", ")}`;

  return (
    <div className="pulse" role="img" aria-label={label}>
      {Array.from({ length: empty }, (_, i) => (
        <span className="tick tick-empty" key={`empty-${i}`} />
      ))}
      {recent.map((t) => {
        const meta = taskStatusMeta(t.status);
        const when = t.finishedAt ?? t.startedAt ?? t.scheduledAt;
        return (
          <span
            key={t.id}
            className={`tick tone-${meta.tone}${meta.spin ? " tick-live" : ""}`}
            data-tip={`#${t.id} ${meta.label}${when ? ` · ${relativeTime(when)}` : ""}`}
          />
        );
      })}
    </div>
  );
}
