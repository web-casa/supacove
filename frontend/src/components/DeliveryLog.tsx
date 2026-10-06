import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Inbox } from "lucide-react";
import { useI18n } from "../i18n";
import { errorMessage } from "../lib/format";
import { notificationsQuery } from "../lib/queries";
import { deliveryMeta } from "../lib/status";
import { StatusBadge } from "./ui/Badge";
import { Button } from "./ui/Button";
import { EmptyState } from "./ui/EmptyState";
import { InlineMessage } from "./ui/InlineMessage";
import { Panel } from "./ui/Panel";
import { RelativeTime } from "./ui/RelativeTime";
import { SkeletonRows } from "./ui/Skeleton";

const PAGE = 10;

// Notification outbox delivery states.
export function DeliveryLog() {
  const { t } = useI18n();
  const notifications = useQuery(notificationsQuery);
  const [showAll, setShowAll] = useState(false);
  const list = notifications.data?.notifications ?? [];
  const visible = showAll ? list : list.slice(0, PAGE);

  return (
    <Panel title={t("dl.title")} description={t("dl.desc")}>
      {notifications.isPending && <SkeletonRows rows={2} />}
      {notifications.isError && !notifications.data && (
        <InlineMessage>
          {t("dl.unavailable", { msg: errorMessage(notifications.error) })}
        </InlineMessage>
      )}
      {notifications.data && list.length === 0 && (
        <EmptyState icon={Inbox} title={t("dl.empty.title")}>
          {t("dl.empty.body")}
        </EmptyState>
      )}
      {list.length > 0 && (
        <div
          className="table table-log stagger"
          role="table"
          aria-label={t("dl.aria")}
        >
          <div className="thead" role="row">
            <span role="columnheader">{t("dl.col.state")}</span>
            <span role="columnheader">{t("dl.col.event")}</span>
            <span role="columnheader">{t("dl.col.database")}</span>
            <span role="columnheader" className="cell-right">
              {t("dl.col.attempts")}
            </span>
            <span role="columnheader" className="cell-right">
              {t("dl.col.created")}
            </span>
          </div>
          {visible.map((n) => {
            const meta = deliveryMeta(n.state);
            return (
              <div
                className={`trow-group tone-${meta.tone}`}
                role="rowgroup"
                key={n.id}
              >
                <div className="trow" role="row">
                  <div className="cell" role="cell">
                    <StatusBadge meta={meta} />
                  </div>
                  <div className="cell cell-main" role="cell">
                    <span className="mono">{n.eventType}</span>
                  </div>
                  <div className="cell" role="cell">
                    <span className="truncate">{n.databaseName}</span>
                  </div>
                  <div className="cell num cell-right" role="cell">
                    {n.attempts}
                  </div>
                  <div className="cell cell-right tip-end" role="cell">
                    <RelativeTime at={n.createdAt} />
                  </div>
                </div>
                {n.lastError && (
                  <div className="trow-extra">
                    <InlineMessage>{n.lastError}</InlineMessage>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
      {list.length > PAGE && (
        <div className="panel-foot">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setShowAll(!showAll)}
          >
            {showAll
              ? t("common.showLatest", { n: PAGE })
              : t("common.showAll", { n: list.length })}
          </Button>
        </div>
      )}
    </Panel>
  );
}
