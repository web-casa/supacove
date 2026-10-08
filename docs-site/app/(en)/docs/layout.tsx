import type { ReactNode } from "react";
import { DocsShell } from "@/routes/docs-layout";

export default function Layout({ children }: { children: ReactNode }) {
  return <DocsShell lang="en">{children}</DocsShell>;
}
