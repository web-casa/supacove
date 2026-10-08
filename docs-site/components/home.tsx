import Link from "next/link";
import { HomeLayout } from "fumadocs-ui/layouts/home";
import { ArrowUpRight } from "lucide-react";
import { baseOptions } from "@/app/layout.config";
import { Features } from "@/components/features";
import { CipherStream } from "@/components/cipher-stream";
import { Hero } from "@/components/hero";
import { Mark } from "@/components/logo";
import { Equation, KeyCard, Poster, Terminal } from "@/components/showcase";
import { homeCopy } from "@/lib/home-copy";
import { localePath } from "@/lib/site";
import { source } from "@/lib/source";
import "@/app/home.css";

const pad = (n: number) => String(n).padStart(2, "0");

/** Renders a title as unbreakable phrases; lines only break between them. */
function Phrases({ parts }: { parts: readonly string[] }) {
  return parts.map((part) => (
    <span key={part} className="lp-phrase">
      {part}
    </span>
  ));
}

/** The landing page. */
export function Home({ lang }: { lang: "zh" | "en" }) {
  const t = homeCopy[lang] ?? homeCopy.en;
  const docs = localePath(lang, "/docs");
  const pages = source.getPages(lang);
  const byUrl = (url: string) => pages.find((p) => p.url === url);
  const tree = source.pageTree[lang].children;
  // Reference pages sit at the top level; guides live in a folder.
  const order = tree.flatMap((n) => (n.type === "page" ? (byUrl(n.url) ?? []) : []));
  const guides = tree.flatMap((n) =>
    n.type === "folder" ? n.children.flatMap((c) => (c.type === "page" ? (byUrl(c.url) ?? []) : [])) : [],
  );

  return (
    <HomeLayout {...baseOptions(lang, { tag: false })} links={[{ text: t.docs, url: docs, active: "nested-url" }]}>
      <div id="nd-page" className="lp">
        <Hero t={t} docs={docs} />

        <ul className="lp-wrap hv-plans">
          {t.plans.map(([who, title, body]) => (
            <li key={who}>
              <span>{who}</span>
              <h2>{title}</h2>
              <p>{body}</p>
              <Link className="lp-link" href={`${docs}/guides/supabase-backup`}>
                {t.plansLink} →
              </Link>
            </li>
          ))}
        </ul>

        <section className="lp-section lp-wrap" aria-labelledby="lp-features">
          <div className="lp-head-row">
            <div className="lp-head">
              <p className="lp-label">
                <b>01</b>
                {t.features.label}
              </p>
              <h2 id="lp-features">
                <Phrases parts={t.features.title} />
              </h2>
            </div>
            <Link className="lp-link" href={docs}>
              {t.features.all} →
            </Link>
          </div>
          <Features t={t} docs={docs} />
        </section>

        <section className="lp-section" aria-labelledby="lp-crypto">
          <div className="lp-wrap sx-split">
            <div>
              <div className="lp-head">
                <p className="lp-label">
                  <b>02</b>
                  {t.crypto.label}
                </p>
                <h2 id="lp-crypto">
                  <Phrases parts={t.crypto.title} />
                </h2>
                <p>{t.crypto.body}</p>
              </div>
              <ul className="sx-points lp-reveal">
                {t.crypto.points.map(([title, body]) => (
                  <li key={title}>
                    <b>{title}</b>
                    <span>{body}</span>
                  </li>
                ))}
              </ul>
              <Link className="lp-link sx-more" href={`${docs}/quickstart`}>
                {t.crypto.link} →
              </Link>
            </div>
            <KeyCard t={t} />
          </div>
          <CipherStream labels={t.stream} />
        </section>

        <section className="lp-section lp-wrap sx-split sx-flip" aria-labelledby="lp-quickstart">
          <div>
            <div className="lp-head">
              <p className="lp-label">
                <b>03</b>
                {t.quickstart.label}
              </p>
              <h2 id="lp-quickstart">
                <Phrases parts={t.quickstart.title} />
              </h2>
              <p>{t.quickstart.body}</p>
            </div>
            <Link className="lp-link sx-more" href={`${docs}/quickstart`}>
              {t.quickstart.link} →
            </Link>
          </div>
          <Terminal t={t} />
        </section>

        <section className="lp-section lp-wrap" aria-labelledby="lp-pipeline">
          <div className="lp-head">
            <p className="lp-label">
              <b>04</b>
              {t.pipeline.label}
            </p>
            <h2 id="lp-pipeline">
              <Phrases parts={t.pipeline.title} />
            </h2>
            <p>{t.pipeline.body}</p>
          </div>
          <ol className="lp-pipe">
            {t.pipeline.stages.map(([name, detail], i) => (
              <li key={name} className="lp-reveal" data-optional={i === 4 || undefined}>
                <span className="lp-pipe-node" />
                <span className="lp-pipe-num">{pad(i + 1)}</span>
                <h3>
                  {name}
                  {i === 4 ? <em>{t.pipeline.optional}</em> : null}
                </h3>
                <p>{i === 0 ? <code>{detail}</code> : detail}</p>
              </li>
            ))}
          </ol>
          <div className="sx-sub lp-reveal">
            <h3>
              <Phrases parts={t.restore.title} />
            </h3>
            <p>{t.restore.body}</p>
          </div>
          <Equation t={t} />
          <div className="lp-restore-foot lp-reveal">
            <ol className="lp-steps">
              {t.restore.steps.map((s, i) => (
                <li key={s}>
                  <span>{pad(i + 1)}</span>
                  {s}
                </li>
              ))}
            </ol>
            <Link className="lp-link" href={`${docs}/restore`}>
              {t.restore.link}
            </Link>
          </div>
        </section>

        <section className="lp-section lp-wrap lp-split" aria-labelledby="lp-guarantees">
          <div className="lp-head lp-sticky">
            <p className="lp-label">
              <b>05</b>
              {t.guarantees.label}
            </p>
            <h2 id="lp-guarantees">
              <Phrases parts={t.guarantees.title} />
            </h2>
          </div>
          <ol className="lp-list">
            {t.guarantees.items.map(([title, body], i) => (
              <li key={title} className="lp-reveal">
                <span className="lp-list-num">{pad(i + 1)}</span>
                <div>
                  <h3>{title}</h3>
                  <p>{body}</p>
                </div>
              </li>
            ))}
          </ol>
        </section>

        <section className="lp-section lp-wrap lp-split" aria-labelledby="lp-faq">
          <div className="lp-head lp-sticky">
            <p className="lp-label">
              <b>06</b>
              {t.faq.label}
            </p>
            <h2 id="lp-faq">
              <Phrases parts={t.faq.title} />
            </h2>
          </div>
          <dl className="lp-faq">
            {t.faq.items.map(([question, answer], i) => (
              <div key={question} className="lp-reveal">
                <dt>{question}</dt>
                <dd>
                  {answer}
                  {i === 0 ? (
                    <small>
                      <a href="https://supabase.com/docs/guides/platform/backups" rel="noopener">
                        {t.faq.source}
                      </a>
                      {" · "}
                      {t.faq.checked}
                    </small>
                  ) : null}
                </dd>
              </div>
            ))}
          </dl>
        </section>

        <section className="lp-section lp-wrap" aria-labelledby="lp-index">
          <div className="lp-head">
            <p className="lp-label">
              <b>07</b>
              {t.index.label}
            </p>
            <h2 id="lp-index">
              <Phrases parts={t.index.title} />
            </h2>
          </div>
          <ol className="lp-toc">
            {order.map((page, i) => (
              <li key={page.url}>
                <Link href={page.url}>
                  <span className="lp-toc-num">{pad(i + 1)}</span>
                  <span className="lp-toc-text">
                    <strong>{page.data.title}</strong>
                    <small>{page.data.description}</small>
                  </span>
                  <ArrowUpRight className="lp-toc-arrow" aria-hidden />
                </Link>
              </li>
            ))}
          </ol>
          {guides.length ? (
            <>
              <p className="lp-label lp-toc-label">{t.index.guides}</p>
              <ol className="lp-toc">
                {guides.map((page, i) => (
                  <li key={page.url}>
                    <Link href={page.url}>
                      <span className="lp-toc-num">{pad(i + 1)}</span>
                      <span className="lp-toc-text">
                        <strong>{page.data.title}</strong>
                        <small>{page.data.description}</small>
                      </span>
                      <ArrowUpRight className="lp-toc-arrow" aria-hidden />
                    </Link>
                  </li>
                ))}
              </ol>
            </>
          ) : null}
        </section>

        <section className="lp-section lp-wrap" aria-labelledby="lp-closing">
          <Poster t={t} docs={docs} />
        </section>

        <footer className="lp-footer">
          <div className="lp-wrap lp-footer-row">
            <span className="lp-footer-brand">
              <Mark size={18} />
              SupaCove
            </span>
            <span>{t.footer.license}</span>
            <a href="#nd-page">{t.footer.top} ↑</a>
          </div>
          <p className="lp-wordmark" aria-hidden>
            SupaCove
          </p>
        </footer>
      </div>
    </HomeLayout>
  );
}
