import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { DatabaseZap, Plus } from "lucide-react";
import { errorMessage } from "../lib/format";
import { overviewQuery, tasksQuery } from "../lib/queries";
import { bySeverity } from "../lib/status";
import { DatabaseRow } from "./DatabaseRow";
import { RegisterDatabase } from "./RegisterDatabase";
import { Button } from "./ui/Button";
import { EmptyState } from "./ui/EmptyState";
import { InlineMessage } from "./ui/InlineMessage";
import { Panel } from "./ui/Panel";
import { SkeletonRows } from "./ui/Skeleton";

// Overview: which databases lack a fresh, verified, successful backup.
export function ProtectionPanel() {
  const overview = useQuery(overviewQuery);
  const tasks = useQuery(tasksQuery);
  const [adding, setAdding] = useState(false);
  const [scheduleFor, setScheduleFor] = useState<number | null>(null);
  const dbs = overview.data ? bySeverity(overview.data.databases) : [];

  return (
    <Panel
      title="Protection overview"
      description="Protection derives from the last successful backup; a failed retry never counts as fresh."
      actions={
        !adding &&
        dbs.length > 0 && (
          <Button variant="primary" size="sm" icon={Plus} onClick={() => setAdding(true)}>
            Add database
          </Button>
        )
      }
    >
      {adding && <RegisterDatabase onClose={() => setAdding(false)} />}

      {overview.isPending && <SkeletonRows />}
      {overview.isError && !overview.data && (
        <InlineMessage>Overview unavailable: {errorMessage(overview.error)}</InlineMessage>
      )}

      {overview.data && dbs.length === 0 && !adding && (
        <EmptyState
          icon={DatabaseZap}
          title="No databases yet"
          action={
            <Button variant="primary" icon={Plus} onClick={() => setAdding(true)}>
              Register your first database
            </Button>
          }
        >
          Connect a Supabase, Neon, Railway or any PostgreSQL database with its connection string.
          Backups are age-encrypted before they leave this server.
        </EmptyState>
      )}

      {dbs.length > 0 && (
        <div className="table table-dbs stagger" role="table" aria-label="Databases">
          <div className="thead" role="row">
            <span role="columnheader">Database</span>
            <span role="columnheader">Protection</span>
            <span role="columnheader">Verification</span>
            <span role="columnheader">Recent runs</span>
            <span role="columnheader">Last success</span>
            <span role="columnheader" className="sr-only">
              Actions
            </span>
          </div>
          {dbs.map((d) => (
            <DatabaseRow
              key={d.databaseId}
              entry={d}
              tasks={(tasks.data?.tasks ?? []).filter((t) => t.databaseId === d.databaseId)}
              scheduleOpen={scheduleFor === d.databaseId}
              onToggleSchedule={() => setScheduleFor(scheduleFor === d.databaseId ? null : d.databaseId)}
              onCloseSchedule={() => setScheduleFor(null)}
            />
          ))}
        </div>
      )}
    </Panel>
  );
}
