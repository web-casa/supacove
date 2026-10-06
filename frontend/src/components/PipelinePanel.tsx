import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import {
  Bell,
  CloudUpload,
  Database,
  FileArchive,
  LockKeyhole,
  ShieldCheck,
  type LucideIcon,
} from "lucide-react";
import { useI18n } from "../i18n";
import { duration, errorMessage, humanBytes, pct } from "../lib/format";
import { statsQuery, tasksQuery } from "../lib/queries";
import type { Tone } from "../lib/status";
import { Beacon } from "./ui/Beacon";
import { CountUp } from "./ui/CountUp";
import { InlineMessage } from "./ui/InlineMessage";
import { Panel } from "./ui/Panel";
import { RelativeTime } from "./ui/RelativeTime";
import { SkeletonText } from "./ui/Skeleton";

const RING_R = 30;
const RING_C = 2 * Math.PI * RING_R;

function rateTone(v?: number | null): Tone | "signal" {
  if (v == null) return "neutral";
  if (v >= 99) return "ok";
  if (v >= 80) return "warn";
  return "danger";
}

interface StageProps {
  icon: LucideIcon;
  name: string;
  hint: string;
  /** Success rate of this stage; `undefined` = the stage has no rate, `null` = no data yet. */
  rate?: number | null;
  loading: boolean;
  children?: ReactNode;
}

/** One pipeline stage: a node whose ring fills with its success rate. */
function Stage({
  icon: Icon,
  name,
  hint,
  rate,
  loading,
  children,
}: StageProps) {
  const hasRate = rate !== undefined;
  const tone = hasRate ? rateTone(rate) : "signal";
  const fill = hasRate ? Math.min(100, Math.max(0, rate ?? 0)) / 100 : 1;
  return (
    <li className={`stage tone-${tone}`}>
      <span className="stage-node" data-tip={hint} tabIndex={0}>
        <svg className="stage-ring" viewBox="0 0 64 64" aria-hidden>
          <circle className="stage-ring-track" cx="32" cy="32" r={RING_R} />
          {!loading && fill > 0 && (
            <circle
              className="stage-ring-fill"
              cx="32"
              cy="32"
              r={RING_R}
              strokeDasharray={RING_C}
              strokeDashoffset={RING_C * (1 - fill)}
            />
          )}
        </svg>
        <Icon size={20} aria-hidden />
      </span>
      <span className="stage-text">
        <span className="stage-name">{name}</span>
        {hasRate && (
          <span className="stage-rate num">
            {loading ? (
              <SkeletonText short />
            ) : rate == null ? (
              "—"
            ) : (
              <CountUp value={rate} format={pct} />
            )}
          </span>
        )}
        <span className="stage-sub">
          {loading ? <SkeletonText short /> : children}
        </span>
      </span>
    </li>
  );
}

const Pipe = () => <li className="pipe" aria-hidden />;

function Stat({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="stat">
      <span
        className="stat-label"
        data-tip={hint}
        tabIndex={hint ? 0 : undefined}
      >
        {label}
      </span>
      <span className="stat-value num">{children}</span>
    </div>
  );
}

// Phase 8 statistics, laid out along the path a backup actually takes:
// the three volume metrics and the segmented success rates sit on the stage
// that produces them.
export function PipelinePanel() {
  const { t } = useI18n();
  const stats = useQuery(statsQuery);
  const tasks = useQuery(tasksQuery);
  const d = stats.data;
  const loading = !d;
  const inFlight = (tasks.data?.tasks ?? []).filter(
    (t) => t.status === "running" || t.status === "pending",
  ).length;
  const count = (n: number) => <CountUp value={n} />;

  return (
    <Panel
      title={t("pipe.title")}
      description={t("pipe.desc")}
      actions={
        <span className={`live-chip${inFlight > 0 ? " live-chip-active" : ""}`}>
          <Beacon
            tone={inFlight > 0 ? "signal" : "neutral"}
            live={inFlight > 0}
          />
          {inFlight > 0 ? t("pipe.inFlight", { n: inFlight }) : t("pipe.idle")}
        </span>
      }
    >
      {stats.isError && !d ? (
        <InlineMessage>
          {t("pipe.unavailable", { msg: errorMessage(stats.error) })}
        </InlineMessage>
      ) : (
        <>
          <ol
            className={`pipeline stagger${inFlight > 0 ? " pipeline-active" : ""}`}
            aria-label={t("pipe.aria")}
          >
            <Stage
              icon={Database}
              name={t("pipe.stage.source")}
              hint={t("pipe.stage.source.hint")}
              loading={loading}
            >
              {d &&
                t("pipe.stage.source.sub", {
                  size: humanBytes(d.totalSourceBytes),
                  count: d.databases,
                })}
            </Stage>
            <Pipe />
            <Stage
              icon={FileArchive}
              name={t("pipe.stage.export")}
              hint={t("pipe.stage.export.hint")}
              rate={d ? (d.exportSuccessRate ?? null) : null}
              loading={loading}
            >
              {t("pipe.stage.export.sub", {
                size: humanBytes(d?.totalDumpBytes),
              })}
            </Stage>
            <Pipe />
            <Stage
              icon={LockKeyhole}
              name={t("pipe.stage.encrypt")}
              hint={t("pipe.stage.encrypt.hint")}
              loading={loading}
            >
              {t("pipe.stage.encrypt.sub", {
                size: humanBytes(d?.totalArtifactBytes),
              })}
            </Stage>
            <Pipe />
            <Stage
              icon={CloudUpload}
              name={t("pipe.stage.remote")}
              hint={t("pipe.stage.remote.hint")}
              rate={d ? (d.remoteSuccessRate ?? null) : null}
              loading={loading}
            >
              {d && t("pipe.stage.remote.sub", { count: d.destinations })}
            </Stage>
            <Pipe />
            <Stage
              icon={ShieldCheck}
              name={t("pipe.stage.verify")}
              hint={t("pipe.stage.verify.hint")}
              rate={d ? (d.verifySuccessRate ?? null) : null}
              loading={loading}
            >
              {t("pipe.stage.verify.sub")}
            </Stage>
            <Pipe />
            <Stage
              icon={Bell}
              name={t("pipe.stage.notify")}
              hint={t("pipe.stage.notify.hint")}
              rate={d ? (d.notifySuccessRate ?? null) : null}
              loading={loading}
            >
              {t("pipe.stage.notify.sub")}
            </Stage>
          </ol>

          <div className="stats">
            <Stat
              label={t("pipe.stat.success")}
              hint={t("pipe.stat.success.hint")}
            >
              {d ? (
                d.successRate == null ? (
                  "—"
                ) : (
                  <CountUp value={d.successRate} format={pct} />
                )
              ) : (
                <SkeletonText short />
              )}
            </Stat>
            <Stat label={t("pipe.stat.jobs")}>
              {d ? count(d.totalJobs) : <SkeletonText short />}
            </Stat>
            <Stat label={t("pipe.stat.succeeded")}>
              {d ? count(d.succeeded) : <SkeletonText short />}
            </Stat>
            <Stat label={t("pipe.stat.failed")}>
              {d ? (
                <span className={d.failed > 0 ? "text-danger" : undefined}>
                  {count(d.failed)}
                </span>
              ) : (
                <SkeletonText short />
              )}
            </Stat>
            <Stat label={t("pipe.stat.avg")} hint={t("pipe.stat.avg.hint")}>
              {d ? duration(d.avgDurationSecs) : <SkeletonText short />}
            </Stat>
            <Stat label={t("pipe.stat.lastSuccess")}>
              {d ? (
                d.lastSuccessAt ? (
                  <RelativeTime at={d.lastSuccessAt} />
                ) : (
                  t("common.never")
                )
              ) : (
                <SkeletonText short />
              )}
            </Stat>
          </div>
        </>
      )}
    </Panel>
  );
}
