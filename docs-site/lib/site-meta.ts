// Home-page title and description per locale, shared by the locale layout
// (defaults) and the home page (canonical, Open Graph, JSON-LD).
export const SITE_META = {
  zh: {
    title: "SupaCove — 自托管的 Supabase / PostgreSQL 加密备份",
    description:
      "SupaCove 用 pg_dump 为 Supabase、Neon、Railway 和任意 PostgreSQL 做定时加密备份，存进你自己的 S3 / R2 / B2 存储桶，恢复不依赖这台实例。",
    locale: "zh_CN",
    skip: "跳到正文",
  },
  en: {
    title: "SupaCove — Self-hosted Supabase & Postgres backup to your own S3",
    description:
      "SupaCove runs scheduled, encrypted pg_dump backups for Supabase, Neon, Railway and any PostgreSQL, stores them in your own S3, R2 or B2 bucket, and restores without the tool.",
    locale: "en_US",
    skip: "Skip to content",
  },
} as const;
