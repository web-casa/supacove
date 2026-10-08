// Feature bento on the landing page: one large card, two stacked beside it
// and three below. Every card is a link to the doc page that covers it, and
// each carries a small illustration built from facts in that page.
import type { ReactNode } from "react";
import Link from "next/link";
import {
  ArrowUpRight,
  CalendarClock,
  Database,
  HeartPulse,
  KeyRound,
  ShieldCheck,
  Webhook,
  type LucideIcon,
} from "lucide-react";
import type { HomeCopy } from "@/lib/home-copy";

// Outbox retry backoff, from notifications.mdx: 30/60/120/240 seconds.
const BACKOFF = [30, 60, 120, 240];

// `decorative` art only repeats the card text, so it is hidden from assistive
// technology; the rest carries facts (retry delays, the setting name).
type Feature = {
  slug: string;
  icon: LucideIcon;
  className?: string;
  decorative?: boolean;
  art: (t: HomeCopy) => ReactNode;
};

// Order matches `features.items` in lib/home-copy.ts.
const FEATURES: Feature[] = [
  {
    slug: "quickstart",
    decorative: true,
    icon: KeyRound,
    className: "ft-big",
    art: () => <span className="ft-age">age</span>,
  },
  {
    slug: "scheduling",
    icon: CalendarClock,
    className: "ft-side",
    art: (t) => (
      <p className="ft-swap">
        <s>{t.features.art.manual}</s>
        <i>→</i>
        <b>0 3 * * *</b>
      </p>
    ),
  },
  {
    slug: "notifications",
    icon: Webhook,
    className: "ft-side",
    art: (t) => (
      <ol className="ft-bars">
        {BACKOFF.map((seconds, i) => (
          <li key={seconds}>
            {t.features.art.retry} {i + 1}
            <span style={{ "--w": `${(seconds / 240) * 100}%` } as React.CSSProperties} />
            <b>{seconds}s</b>
          </li>
        ))}
      </ol>
    ),
  },
  {
    slug: "heartbeat",
    decorative: true,
    icon: HeartPulse,
    art: () => (
      <span className="ft-beat">
        <HeartPulse aria-hidden />
      </span>
    ),
  },
  {
    slug: "databases",
    decorative: true,
    icon: Database,
    art: (t) => (
      <ul className="ft-chips">
        {t.features.art.platforms.map((name) => (
          <li key={name}>{name}</li>
        ))}
        {t.features.art.more.map((name) => (
          <li key={name} className="ft-chip-more">
            {name}
          </li>
        ))}
      </ul>
    ),
  },
  {
    slug: "configuration",
    icon: ShieldCheck,
    art: () => (
      <p className="ft-toggle">
        <span>SB_VERIFY_ENABLED</span>
        <i />
      </p>
    ),
  },
];

export function Features({ t, docs }: { t: HomeCopy; docs: string }) {
  return (
    <div className="ft-grid">
      {FEATURES.map(({ slug, icon: Icon, className, decorative, art }, i) => {
        const [title, body] = t.features.items[i];
        return (
          <Link key={slug} href={`${docs}/${slug}`} className={`ft-card lp-reveal ${className ?? ""}`}>
            <div className="ft-art" aria-hidden={decorative}>
              {art(t)}
            </div>
            <div className="ft-meta">
              <h3>
                <Icon aria-hidden />
                {title}
              </h3>
              <p>{body}</p>
            </div>
            <ArrowUpRight className="ft-go" aria-hidden />
          </Link>
        );
      })}
    </div>
  );
}
