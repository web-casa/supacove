import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Bell, CloudUpload, Database, FileArchive, LockKeyhole, ShieldCheck, type LucideIcon } from "lucide-react";
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
function Stage({ icon: Icon, name, hint, rate, loading, children }: StageProps) {
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
            {loading ? <SkeletonText short /> : rate == null ? "—" : <CountUp value={rate} format={pct} />}
          </span>
        )}
        <span className="stage-sub">{loading ? <SkeletonText short /> : children}</span>
      </span>
    </li>
  );
}

const Pipe = () => <li className="pipe" aria-hidden />;

function Stat({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="stat">
      <span className="stat-label" data-tip={hint} tabIndex={hint ? 0 : undefined}>
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
  const stats = useQuery(statsQuery);
  const tasks = useQuery(tasksQuery);
  const d = stats.data;
  const loading = !d;
  const inFlight = (tasks.data?.tasks ?? []).filter((t) => t.status === "running" || t.status === "pending").length;
  const count = (n: number) => <CountUp value={n} />;

  return (
    <Panel
      title="Pipeline & statistics"
      description="Every backup takes this path. Rings show lifetime success per stage."
      actions={
        <span className={`live-chip${inFlight > 0 ? " live-chip-active" : ""}`}>
          <Beacon tone={inFlight > 0 ? "signal" : "neutral"} live={inFlight > 0} />
          {inFlight > 0 ? `${inFlight} in flight` : "idle"}
        </span>
      }
    >
      {stats.isError && !d ? (
        <InlineMessage>Statistics unavailable: {errorMessage(stats.error)}</InlineMessage>
      ) : (
        <>
          <ol className={`pipeline stagger${inFlight > 0 ? " pipeline-active" : ""}`} aria-label="Backup pipeline stages">
            <Stage icon={Database} name="Source" hint="Newest known physical size per database, summed" loading={loading}>
              {d && (
                <>
                  <span className="num">{humanBytes(d.totalSourceBytes)}</span> · {d.databases}{" "}
                  {d.databases === 1 ? "database" : "databases"}
                </>
              )}
            </Stage>
            <Pipe />
            <Stage
              icon={FileArchive}
              name="Export"
              hint="pg_dump success rate; size = compressed archives recorded in statistics"
              rate={d ? (d.exportSuccessRate ?? null) : null}
              loading={loading}
            >
              <span className="num">{humanBytes(d?.totalDumpBytes)}</span> dump archive
            </Stage>
            <Pipe />
            <Stage icon={LockKeyhole} name="Encrypt" hint="age ciphertext across succeeded backups" loading={loading}>
              <span className="num">{humanBytes(d?.totalArtifactBytes)}</span> encrypted
            </Stage>
            <Pipe />
            <Stage
              icon={CloudUpload}
              name="Remote commit"
              hint="Remote commits vs upload failures"
              rate={d ? (d.remoteSuccessRate ?? null) : null}
              loading={loading}
            >
              {d && (
                <>
                  {d.destinations} {d.destinations === 1 ? "destination" : "destinations"}
                </>
              )}
            </Stage>
            <Pipe />
            <Stage
              icon={ShieldCheck}
              name="Verification"
              hint="Verified vs failed/unsupported restores"
              rate={d ? (d.verifySuccessRate ?? null) : null}
              loading={loading}
            >
              restore-tested
            </Stage>
            <Pipe />
            <Stage
              icon={Bell}
              name="Notification"
              hint="Delivered vs dead webhook deliveries"
              rate={d ? (d.notifySuccessRate ?? null) : null}
              loading={loading}
            >
              webhook delivery
            </Stage>
          </ol>

          <div className="stats">
            <Stat label="Success rate" hint="Succeeded / finished jobs">
              {d ? d.successRate == null ? "—" : <CountUp value={d.successRate} format={pct} /> : <SkeletonText short />}
            </Stat>
            <Stat label="Total jobs">{d ? count(d.totalJobs) : <SkeletonText short />}</Stat>
            <Stat label="Succeeded">{d ? count(d.succeeded) : <SkeletonText short />}</Stat>
            <Stat label="Failed">
              {d ? <span className={d.failed > 0 ? "text-danger" : undefined}>{count(d.failed)}</span> : <SkeletonText short />}
            </Stat>
            <Stat label="Avg duration" hint="Dump through remote commit, succeeded jobs">
              {d ? duration(d.avgDurationSecs) : <SkeletonText short />}
            </Stat>
            <Stat label="Last success">
              {d ? d.lastSuccessAt ? <RelativeTime at={d.lastSuccessAt} /> : "never" : <SkeletonText short />}
            </Stat>
          </div>
        </>
      )}
    </Panel>
  );
}
