import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { DocsBody, DocsPage, DocsTitle } from "fumadocs-ui/layouts/docs/page";
import { JsonLd } from "@/components/json-ld";
import { getMDXComponents } from "@/mdx-components";
import type { Lang } from "@/lib/i18n";
import { SITE_META } from "@/lib/site-meta";
import { OG_IMAGE, SITE_NAME, SITE_URL, absoluteUrl, alternates } from "@/lib/site";
import { source } from "@/lib/source";

type Props = { params: Promise<{ slug?: string[] }> };

const STRINGS = {
  zh: { updated: "最后更新", home: "首页", docs: "文档", dateLocale: "zh-CN" },
  en: { updated: "Last updated", home: "Home", docs: "Docs", dateLocale: "en-US" },
} as const;

/** Locale-independent path of a doc: slug ["quickstart"] → "/docs/quickstart". */
const docPath = (slug?: string[]) => `/docs${slug?.length ? `/${slug.join("/")}` : ""}`;

export async function DocsPageView({ lang, params: promise }: Props & { lang: Lang }) {
  const params = { lang, ...(await promise) };
  const page = source.getPage(params.slug, params.lang);
  if (!page) notFound();

  // Position in sidebar reading order, shown as "03 / 12" above the title.
  const order = source.pageTree[params.lang].children.filter((n) => n.type === "page");
  const index = order.findIndex((n) => n.url === page.url);
  const pad = (n: number) => String(n).padStart(2, "0");

  const MDX = page.data.body;
  const s = STRINGS[params.lang];
  const path = docPath(params.slug);
  const url = absoluteUrl(params.lang, path);
  const modified = page.data.lastModified;
  return (
    <DocsPage toc={page.data.toc} full={page.data.full} tableOfContent={{ style: "clerk" }}>
      {index >= 0 ? (
        <p className="sb-page-eyebrow">
          <span>
            <b>{pad(index + 1)}</b> / {pad(order.length)}
          </span>
        </p>
      ) : null}
      <DocsTitle>{page.data.title}</DocsTitle>
      {page.data.description ? <p className="sb-page-lede">{page.data.description}</p> : null}
      <DocsBody>
        <MDX components={getMDXComponents()} />
      </DocsBody>
      {modified ? (
        <p className="sb-page-updated">
          {s.updated}{" "}
          <time dateTime={modified.toISOString()}>
            {modified.toLocaleDateString(s.dateLocale, { year: "numeric", month: "long", day: "numeric", timeZone: "UTC" })}
          </time>
        </p>
      ) : null}
      <JsonLd
        data={{
          "@context": "https://schema.org",
          "@graph": [
            {
              "@type": "TechArticle",
              "@id": `${url}#article`,
              headline: page.data.title,
              description: page.data.description,
              url,
              inLanguage: params.lang,
              ...(modified ? { dateModified: modified.toISOString() } : {}),
              isPartOf: { "@id": `${SITE_URL}/#website` },
              about: { "@id": `${SITE_URL}/#software` },
            },
            {
              "@type": "BreadcrumbList",
              itemListElement: [
                { "@type": "ListItem", position: 1, name: s.home, item: absoluteUrl(params.lang) },
                { "@type": "ListItem", position: 2, name: s.docs, item: absoluteUrl(params.lang, "/docs") },
                // The docs index is its own second crumb; deeper pages add a third.
                ...(params.slug?.length ? [{ "@type": "ListItem", position: 3, name: page.data.title, item: url }] : []),
              ],
            },
          ],
        }}
      />
    </DocsPage>
  );
}

export function docsStaticParams(lang: Lang) {
  return source
    .generateParams()
    .filter((p) => p.lang === lang)
    .map(({ slug }) => ({ slug }));
}

export async function docsMetadata(lang: Lang, props: Props): Promise<Metadata> {
  const params = { lang, ...(await props.params) };
  const page = source.getPage(params.slug, params.lang);
  if (!page) notFound();
  const path = docPath(params.slug);
  const title = page.data.title;
  const description = page.data.description;
  return {
    title,
    description,
    alternates: alternates(params.lang, path),
    openGraph: {
      type: "article",
      siteName: SITE_NAME,
      title: `${title} · ${SITE_NAME}`,
      description,
      url: absoluteUrl(params.lang, path),
      locale: SITE_META[params.lang].locale,
      images: [OG_IMAGE],
    },
    twitter: { card: "summary_large_image", title: `${title} · ${SITE_NAME}`, description, images: [OG_IMAGE] },
  };
}
