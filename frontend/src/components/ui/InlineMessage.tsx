import type { ReactNode } from "react";
import { CircleAlert, CircleCheck, Info, TriangleAlert } from "lucide-react";
import type { Tone } from "../../lib/status";

const icons = { danger: CircleAlert, warn: TriangleAlert, ok: CircleCheck, neutral: Info, info: Info };

interface Props {
  tone?: Tone;
  /** Banner = boxed, for messages that must not be missed (pooling warning). */
  banner?: boolean;
  children: ReactNode;
}

export function InlineMessage({ tone = "danger", banner, children }: Props) {
  const Icon = icons[tone];
  return (
    <p
      className={`inline-msg tone-${tone}${banner ? " inline-msg-banner" : ""}`}
      role={tone === "danger" ? "alert" : "status"}
    >
      <Icon size={14} aria-hidden />
      <span>{children}</span>
    </p>
  );
}
