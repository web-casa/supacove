import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { useI18n } from "../../i18n";
import type { StatusMeta, Tone } from "../../lib/status";

interface Props {
  tone?: Tone;
  icon?: LucideIcon;
  spin?: boolean;
  children: ReactNode;
}

export function Badge({ tone = "neutral", icon: Icon, spin, children }: Props) {
  return (
    <span className={`badge tone-${tone}`}>
      {Icon && (
        <Icon size={12} className={spin ? "spin" : undefined} aria-hidden />
      )}
      {children}
    </span>
  );
}

export function StatusBadge({ meta }: { meta: StatusMeta }) {
  const { t } = useI18n();
  return (
    <Badge tone={meta.tone} icon={meta.icon} spin={meta.spin}>
      {t(meta.label)}
    </Badge>
  );
}
