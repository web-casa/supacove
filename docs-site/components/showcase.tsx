// Illustrations used inside the landing sections: the identity key card, the
// quickstart terminal, the restore equation and the closing poster. Each is
// built from facts in the docs page it sits next to. Styled by .hv-* in
// app/home.css.
import { Fragment, type ReactNode } from "react";
import Link from "next/link";
import { ArrowRight, Database, FileLock2, FileTerminal, KeyRound } from "lucide-react";
import { Mark } from "@/components/logo";
import type { HomeCopy } from "@/lib/home-copy";

/** identity.txt as a tilted card; the secret itself is only redaction blocks. */
export function KeyCard({ t }: { t: HomeCopy }) {
  const k = t.crypto.card;
  return (
    <div className="hv-key lp-reveal" aria-hidden>
      <div className="hv-key-back">
        <span>recipient</span>
        <code>age1…</code>
        <small>{k.recipient}</small>
      </div>
      <div className="hv-key-card">
        <p className="hv-key-head">
          <KeyRound />
          identity.txt
          <em>{k.offline}</em>
        </p>
        <pre>
          <span># created: …</span>
          <span># public key: age1…</span>
          <span className="hv-key-secret">
            AGE-SECRET-KEY-1
            <i />
          </span>
        </pre>
        <p className="hv-key-foot">{k.once}</p>
      </div>
    </div>
  );
}

// Verbatim from the quickstart (content/docs/*/quickstart.mdx, steps 1–3).
const COMMANDS: string[][] = [
  ["docker build --target runtime -t supacove:local ."],
  ["docker run -d --name supacove \\", "  -p 127.0.0.1:8080:8080 \\", "  -v supacove-data:/app/data \\", "  supacove:local"],
  ["docker exec supacove /app/supacove bootstrap"],
];
const OUTPUT = ["One-time bootstrap token (valid 15m0s):", "", "  jkwioY…"];

/** The three real quickstart commands, then the three steps that follow. */
export function Terminal({ t }: { t: HomeCopy }) {
  const term = t.quickstart.term;
  let n = 0;
  const line = (className: string, children: ReactNode) => (
    <span key={n++} className={`hv-term-line ${className}`}>
      {children}
    </span>
  );
  return (
    <div className="hv-term lp-reveal">
      <p className="hv-term-tab">
        <span>{term.tab}</span>
        <span>01–03 / 06</span>
      </p>
      <pre>
        {COMMANDS.flatMap((cmd, i) => [
          line("hv-term-comment", `# ${i + 1}. ${term.comments[i]}`),
          ...cmd.map((text, k) => line(k === 0 ? "hv-term-cmd" : "hv-term-cont", text)),
        ])}
        {OUTPUT.map((text) => line("hv-term-out", text || " "))}
        {line("hv-term-cmd hv-term-caret", "")}
      </pre>
      <ol className="hv-term-next">
        <li>{term.nextLabel}</li>
        {term.next.map((step, i) => (
          <li key={step}>
            <b>0{i + 4}</b>
            {step}
          </li>
        ))}
      </ol>
    </div>
  );
}

const PART_ICONS = [FileLock2, KeyRound, FileTerminal];

/** What a restore needs: three files that add up to a database. */
export function Equation({ t }: { t: HomeCopy }) {
  return (
    <div className="hv-stage-card lp-reveal">
      {t.restore.parts.map(([file, what], i) => {
        const Icon = PART_ICONS[i];
        return (
          <Fragment key={file}>
            {i > 0 ? <span className="hv-op">+</span> : null}
            <div className="hv-file">
              <Icon aria-hidden />
              <code>{file}</code>
              <small>{what}</small>
            </div>
          </Fragment>
        );
      })}
      <span className="hv-op">=</span>
      <div className="hv-file hv-file-result">
        <Database aria-hidden />
        <strong>{t.restore.result}</strong>
        <small>pg_restore</small>
      </div>
    </div>
  );
}

/** Closing call to action: one soft card with the mark blown up in it. */
export function Poster({ t, docs }: { t: HomeCopy; docs: string }) {
  const c = t.closing;
  return (
    <div className="hv-poster lp-reveal">
      <div className="hv-poster-mark" aria-hidden>
        <Mark size={24} />
      </div>
      <p className="lp-eyebrow">
        <i className="lp-pulse" />
        {c.label}
      </p>
      <h2 id="lp-closing">
        <span>{c.title[0]}</span>
        <span className="lp-title-dim">{c.title[1]}</span>
      </h2>
      <p className="lp-lede">{c.body}</p>
      <div className="lp-actions">
        <Link className="lp-btn" href={`${docs}/quickstart`}>
          {t.primary}
          <span className="lp-btn-arrow">
            <ArrowRight aria-hidden />
          </span>
        </Link>
        <Link className="lp-link" href={`${docs}/restore`}>
          {t.restore.link}
        </Link>
      </div>
      <dl className="hv-poster-facts">
        {t.facts.map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
