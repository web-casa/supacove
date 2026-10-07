import type { ReactNode } from "react";
import { i18n } from "@/lib/i18n";
import "./global.css";

// The locale-aware provider lives in app/[lang]/layout.tsx (it needs the
// segment param); the root layout only owns <html>/<body>.
// <html lang> is set per locale inside the [lang] segment layout (a nested
// html element is invalid); the root keeps no language claim of its own.
export default function Layout({ children }: { children: ReactNode }) {
  return (
    <html suppressHydrationWarning>
      <body className="flex flex-col min-h-screen">{children}</body>
    </html>
  );
}
