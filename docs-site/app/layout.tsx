import type { ReactNode } from "react";
import "./global.css";
import "./global.css";

// Language-free root shell: the per-locale <html lang> lives in
// app/[lang]/layout.tsx so assistive tech follows the route.
export default function Layout({ children }: { children: ReactNode }) {
  return <>{children}</>;
}
