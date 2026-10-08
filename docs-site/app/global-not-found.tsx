// The 404 document for every unmatched URL (experimental.globalNotFound in
// next.config.mjs). It replaces per-locale not-found pages: with one root
// layout per locale there is no shared layout to render them in, and this
// file is served as plain HTML, so the page reads without JavaScript.
// It bypasses the layouts, hence its own styles, fonts, <html> and theme.
import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import { SITE_NAME, SITE_URL } from "@/lib/site";
import "./global.css";

const sans = Geist({ subsets: ["latin"], variable: "--font-geist-sans", display: "swap" });
const mono = Geist_Mono({ subsets: ["latin"], variable: "--font-geist-mono", display: "swap" });

export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: `Page not found · ${SITE_NAME}`,
  robots: { index: false },
};

// No theme provider runs here: follow the OS setting before first paint.
// The HTML carries both languages; under /zh the same script picks Chinese.
// Without JavaScript both stay visible.
const BOOT = `var d=document.documentElement,p=location.pathname,zh=p==="/zh"||p.indexOf("/zh/")===0;if(matchMedia("(prefers-color-scheme: dark)").matches)d.classList.add("dark");d.lang=zh?"zh":"en";d.dataset.lang=d.lang;if(zh)document.title="页面不存在 · SupaCove"`;

export default function GlobalNotFound() {
  return (
    <html lang="en" className={`${sans.variable} ${mono.variable}`} suppressHydrationWarning>
      <body>
        <script dangerouslySetInnerHTML={{ __html: BOOT }} />
        <main className="sb-404" id="nd-page">
          <span className="sb-404-code">404 · not found</span>
          <section lang="en">
            <h1>No backup of this page.</h1>
            <p>The address may have been renamed, or it never existed. Head back to the docs and pick up from there.</p>
            <a href="/docs">Back to the docs →</a>
          </section>
          <section lang="zh">
            <h1>这一页没有备份。</h1>
            <p>地址可能已经改名，或者从未存在。回到文档目录，从那里继续。</p>
            <a href="/zh/docs">回到文档 →</a>
          </section>
        </main>
      </body>
    </html>
  );
}
