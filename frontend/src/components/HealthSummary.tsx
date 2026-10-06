import { useQuery } from "@tanstack/react-query";
import {
  PauseCircle,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
  ShieldOff,
  type LucideIcon,
} from "lucide-react";
import { useI18n } from "../i18n";
import { errorMessage } from "../lib/format";
import { spotlight } from "../lib/motion";
import { overviewQuery, statsQuery } from "../lib/queries";
import {
  bySeverity,
  summarize,
  type HealthSummary as Summary,
  type Tone,
} from "../lib/status";
import { VitalsStrip } from "./VitalsStrip";
import { Beacon } from "./ui/Beacon";
import { Button } from "./ui/Button";
import { CountUp } from "./ui/CountUp";
import { InlineMessage } from "./ui/InlineMessage";
import { RelativeTime } from "./ui/RelativeTime";
import { SkeletonText } from "./ui/Skeleton";

interface TileProps {
  tone: Tone;
  icon: LucideIcon;
  label: string;
  value: number | undefined;
}

function Tile({ tone, icon: Icon, label, value }: TileProps) {
  return (
    <div className={`tile tone-${tone}${value ? " tile-active" : ""}`}>
      <span className="tile-label">
        <Icon size={13} aria-hidden />
        {label}
      </span>
      <span className="tile-value num">
        {value === undefined ? (
          <SkeletonText short />
        ) : (
          <CountUp value={value} />
        )}
      </span>
    </div>
  );
}

/** First-screen answer to "is everything OK?". */
export function HealthSummary() {
  const { t } = useI18n();
  const overview = useQuery(overviewQuery);
  const stats = useQuery(statsQuery);
  const s = overview.data ? summarize(overview.data.databases) : undefined;
  const tone = s?.tone ?? "neutral";
  const lastSuccessAt = stats.data?.lastSuccessAt || s?.lastSuccessAt || null;

  const headline = (v: Summary): string => {
    if (v.total === 0) return t("health.headline.none");
    if (v.needAttention === 0)
      return v.total === 1
        ? t("health.headline.protectedOne")
        : t("health.headline.protectedAll", { n: v.total });
    return t("health.headline.attention", {
      need: v.needAttention,
      total: v.total,
      count: v.total,
    });
  };
  const detail = (v: Summary): string => {
    if (v.total === 0) return t("health.detail.none");
    const parts = [
      v.expired > 0 && t("health.detail.expired", { n: v.expired }),
      v.never > 0 && t("health.detail.never", { n: v.never }),
      v.verifyFailed > 0 &&
        t("health.detail.verifyFailed", { n: v.verifyFailed }),
      v.lastJobFailed > 0 &&
        t("health.detail.lastJobFailed", { n: v.lastJobFailed }),
    ].filter(Boolean);
    return parts.length > 0 ? parts.join(" · ") : t("health.detail.allGood");
  };

  return (
    <section
      className={`health spotlight tone-${tone}`}
      aria-label={t("health.aria")}
      onPointerMove={spotlight}
    >
      <div className="health-head">
        <div className="health-text">
          <p className="eyebrow">
            <Beacon tone={tone} live={tone !== "neutral"} />
            {t("health.title")}
          </p>
          <h1>{s ? headline(s) : <SkeletonText />}</h1>
          <p className="muted">
            {s
              ? detail(s)
              : overview.isError
                ? t("health.detail.unknown")
                : t("health.detail.loading")}
          </p>
        </div>
        <div className="health-last tip-end">
          <span className="muted">{t("health.lastSuccess")}</span>
          <strong>
            {lastSuccessAt ? (
              <RelativeTime at={lastSuccessAt} />
            ) : s || stats.data ? (
              t("common.never")
            ) : (
              <SkeletonText short />
            )}
          </strong>
        </div>
        <Button
          variant="ghost"
          size="sm"
          icon={RefreshCw}
          tip={t("health.refreshTip")}
          className="tip-end"
          loading={overview.isFetching && !overview.isPending}
          onClick={() => {
            overview.refetch();
            stats.refetch();
          }}
        />
      </div>

      {overview.isError && (
        <InlineMessage>
          {t("health.unavailable", { msg: errorMessage(overview.error) })}
        </InlineMessage>
      )}

      {overview.data && s && s.total > 0 && (
        <VitalsStrip dbs={bySeverity(overview.data.databases)} />
      )}

      <div className="tiles stagger">
        <Tile
          tone="ok"
          icon={ShieldCheck}
          label={t("health.tile.protected")}
          value={s?.fresh}
        />
        <Tile
          tone="danger"
          icon={ShieldAlert}
          label={t("health.tile.expired")}
          value={s?.expired}
        />
        <Tile
          tone="warn"
          icon={ShieldOff}
          label={t("health.tile.never")}
          value={s?.never}
        />
        <Tile
          tone="neutral"
          icon={PauseCircle}
          label={t("health.tile.paused")}
          value={s?.paused}
        />
      </div>
    </section>
  );
}
