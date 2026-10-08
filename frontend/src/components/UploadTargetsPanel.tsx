import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type Database, type Destination, api } from "../api/client";
import { useI18n } from "../i18n";
import { errorMessage } from "../lib/format";
import { databasesQuery, destinationsQuery } from "../lib/queries";
import { useToast } from "../lib/toast";
import { InlineMessage } from "./ui/InlineMessage";
import { Panel } from "./ui/Panel";
import { SkeletonRows } from "./ui/Skeleton";
import { Spinner } from "./ui/Spinner";

function TargetRow({
  db,
  destinations,
  ready,
}: {
  db: Database;
  destinations: Destination[];
  /** False until the destination list has loaded: nothing can be chosen. */
  ready: boolean;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const { t } = useI18n();
  const assign = useMutation({
    mutationFn: (destinationId: number | null) =>
      api.assignDestination(db.id, destinationId),
    onSuccess: (_, destinationId) => {
      const dest = destinations.find((d) => d.id === destinationId);
      toast(
        "ok",
        dest
          ? t("target.toast.assigned", { db: db.name, dest: dest.name })
          : t("target.toast.cleared", { db: db.name }),
      );
    },
    // Also after a refusal: the select must show what the server holds.
    onSettled: () => qc.invalidateQueries({ queryKey: ["databases"] }),
  });
  const selectId = `target-${db.id}`;
  const current = assign.isPending
    ? (assign.variables ?? null)
    : (db.destinationId ?? null);
  // An id the list does not hold (still loading, or failed to load) gets its
  // own option, so the select never falls back to showing "None".
  const unresolved =
    current != null && !destinations.some((d) => d.id === current);

  return (
    <div className="trow-group" role="rowgroup">
      <div className="trow" role="row">
        <div className="cell cell-main" role="cell">
          <label className="db-name truncate" htmlFor={selectId}>
            {db.name}
          </label>
          <span className="chip">{db.platform}</span>
        </div>
        <div className="cell" role="cell">
          <select
            id={selectId}
            value={current ?? ""}
            disabled={!ready || assign.isPending}
            onChange={(e) =>
              assign.mutate(
                e.target.value === "" ? null : Number(e.target.value),
              )
            }
          >
            <option value="">{t("target.localOnly")}</option>
            {unresolved && (
              <option value={current}>
                {t("target.unresolved", { id: current })}
              </option>
            )}
            {destinations.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name} · {d.bucket}/{d.prefix ?? ""}
              </option>
            ))}
          </select>
        </div>
        <div className="cell" role="cell">
          {assign.isPending ? (
            <Spinner size={12} />
          ) : (
            <span className="muted">
              {db.destinationId == null
                ? t("target.note.local")
                : t("target.note.remote")}
            </span>
          )}
        </div>
      </div>
      {assign.error && (
        <div className="trow-extra">
          <InlineMessage>{errorMessage(assign.error)}</InlineMessage>
        </div>
      )}
    </div>
  );
}

// Which destination each database uploads to.
export function UploadTargetsPanel() {
  const { t } = useI18n();
  const dbs = useQuery(databasesQuery);
  const dests = useQuery(destinationsQuery);
  const list = dbs.data?.databases ?? [];

  return (
    <Panel title={t("target.title")} description={t("target.desc")}>
      {dbs.isPending && <SkeletonRows rows={2} />}
      {dbs.isError && !dbs.data && (
        <InlineMessage>
          {t("health.unavailable", { msg: errorMessage(dbs.error) })}
        </InlineMessage>
      )}
      {dests.isError && !dests.data && (
        <InlineMessage>
          {t("dest.unavailable", { msg: errorMessage(dests.error) })}
        </InlineMessage>
      )}
      {dbs.data && list.length === 0 && (
        <p className="panel-note muted">{t("target.empty")}</p>
      )}
      {list.length > 0 && (
        <div
          className="table table-targets stagger"
          role="table"
          aria-label={t("target.aria")}
        >
          <div className="thead" role="row">
            <span role="columnheader">{t("table.col.database")}</span>
            <span role="columnheader">{t("target.col.destination")}</span>
            <span role="columnheader">{t("target.col.effect")}</span>
          </div>
          {list.map((d) => (
            <TargetRow
              key={d.id}
              db={d}
              destinations={dests.data?.destinations ?? []}
              ready={dests.data !== undefined}
            />
          ))}
        </div>
      )}
    </Panel>
  );
}
