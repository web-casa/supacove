import type { ReactNode } from "react";
import { i18n } from "@/lib/i18n";
import "./global.css";

// The locale-aware provider lives in app/[lang]/layout.tsx (it needs the
// segment param); the root layout only owns <html>/<body>.
export default function Layout({ children }: { children: ReactNode }) {
  return (
    <html lang={i18n.defaultLanguage} suppressHydrationWarning>
      <body className="flex flex-col min-h-screen">{children}</body>
    </html>
  );
}
