// The landing hero: a centred headline over the data path — four kinds of
// source in, four kinds of object storage out. Styled by .hv-* in home.css.
import { Fragment } from "react";
import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { Mark } from "@/components/logo";
import { Rotator } from "@/components/rotator";
import type { HomeCopy } from "@/lib/home-copy";

// The platform named in the headline, swapped every two seconds.
const PLATFORMS = ["Supabase", "Neon", "Aiven", "Prisma Postgres", "TigerData", "Miget", "Railway", "Render"];

/** One headline line, with {platform} replaced by the rotating name. */
function Line({ text }: { text: string }) {
  const [before, after] = text.split("{platform}");
  if (after === undefined) return text;
  return (
    <>
      {before}
      <Rotator words={PLATFORMS} />
      {after}
    </>
  );
}

// Wire ends, as a percentage of the column height: one per chip.
const WIRES = [12.5, 37.5, 62.5, 87.5];

function Wires({ out = false }: { out?: boolean }) {
  return (
    <svg className="hv-flow-wires" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden>
      {WIRES.map((y) => {
        const d = out ? `M0 50C50 50 50 ${y} 100 ${y}` : `M0 ${y}C50 ${y} 50 50 100 50`;
        return (
          <Fragment key={y}>
            <path d={d} />
            <path className="hv-flow-dash" d={d} />
          </Fragment>
        );
      })}
    </svg>
  );
}

export function Hero({ t, docs }: { t: HomeCopy; docs: string }) {
  return (
    <header className="lp-hero lp-wrap hv-c hv-flowhero">
      <p className="lp-eyebrow">
        <i className="lp-pulse" />
        {t.eyebrow}
      </p>
      <h1 className="lp-title" aria-label={t.titleLabel}>
        <span aria-hidden>
          <Line text={t.title[0]} />
        </span>
        <span className="lp-title-dim" aria-hidden>
          <Line text={t.title[1]} />
        </span>
      </h1>
      <p className="lp-lede">{t.lede}</p>
      <div className="lp-actions">
        <Link className="lp-btn" href={`${docs}/quickstart`}>
          {t.primary}
          <span className="lp-btn-arrow">
            <ArrowRight aria-hidden />
          </span>
        </Link>
        <Link className="lp-link" href={`${docs}/restore`}>
          {t.secondary}
        </Link>
      </div>
      <div className="hv-flow" aria-hidden>
        <ul className="hv-flow-col" data-label={t.flow.source}>
          {t.features.art.platforms.map((name) => (
            <li key={name}>
              <span>{name}</span>
            </li>
          ))}
        </ul>
        <Wires />
        <div className="hv-flow-core">
          <Mark size={30} />
          <b>SupaCove</b>
          <ol>
            {t.flow.stages.map((stage) => (
              <li key={stage}>{stage}</li>
            ))}
          </ol>
        </div>
        <Wires out />
        <ul className="hv-flow-col" data-label={t.flow.storage}>
          {t.flow.stores.map((name) => (
            <li key={name}>
              <span>{name}</span>
            </li>
          ))}
        </ul>
      </div>
      <p className="hv-flow-also">{t.flow.also}</p>
    </header>
  );
}
