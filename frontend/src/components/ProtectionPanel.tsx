import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { DatabaseZap, Plus } from "lucide-react";
import { useI18n } from "../i18n";
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
  const { t } = useI18n();
  const overview = useQuery(overviewQuery);
  const tasks = useQuery(tasksQuery);
  const [adding, setAdding] = useState(false);
  const [scheduleFor, setScheduleFor] = useState<number | null>(null);
  const dbs = overview.data ? bySeverity(overview.data.databases) : [];

  return (
    <Panel
      title={t("panel.protection.title")}
      description={t("panel.protection.desc")}
      actions={
        !adding &&
        dbs.length > 0 && (
          <Button variant="primary" size="sm" icon={Plus} onClick={() => setAdding(true)}>
            {t("panel.protection.add")}
          </Button>
        )
      }
    >
      {adding && <RegisterDatabase onClose={() => setAdding(false)} />}

      {overview.isPending && <SkeletonRows />}
      {overview.isError && !overview.data && (
        <InlineMessage>{t("health.unavailable", { msg: errorMessage(overview.error) })}</InlineMessage>
      )}

      {overview.data && dbs.length === 0 && !adding && (
        <EmptyState
          icon={DatabaseZap}
          title={t("panel.protection.empty.title")}
          action={
            <Button variant="primary" icon={Plus} onClick={() => setAdding(true)}>
              {t("panel.protection.empty.cta")}
            </Button>
          }
        >
          {t("panel.protection.empty.body")}
        </EmptyState>
      )}

      {dbs.length > 0 && (
        <div className="table table-dbs stagger" role="table" aria-label={t("table.aria.databases")}>
          <div className="thead" role="row">
            <span role="columnheader">{t("table.col.database")}</span>
            <span role="columnheader">{t("table.col.protection")}</span>
            <span role="columnheader">{t("table.col.verification")}</span>
            <span role="columnheader">{t("table.col.recentRuns")}</span>
            <span role="columnheader">{t("table.col.lastSuccess")}</span>
            <span role="columnheader" className="sr-only">
              {t("table.col.actions")}
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
