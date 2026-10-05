import { useEffect, useRef, useState } from "react";
import type { LucideIcon } from "lucide-react";
import { useI18n } from "../../i18n";
import { Button } from "./Button";

interface Props {
  /** Trigger label; omit for an icon-only trigger (then `tip` names it). */
  label?: string;
  tip?: string;
  icon: LucideIcon;
  /** Question shown once armed, e.g. `Remove "prod"?`. */
  prompt: string;
  confirmLabel: string;
  pending?: boolean;
  onConfirm: () => void;
}

/** Two-step destructive action: the first click only arms it. */
export function ConfirmButton({ label, tip, icon, prompt, confirmLabel, pending, onConfirm }: Props) {
  const { t } = useI18n();
  const [armed, setArmed] = useState(false);
  const cancelRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (armed) cancelRef.current?.focus();
  }, [armed]);

  if (!armed && !pending) {
    return (
      <Button variant="ghost" size="sm" icon={icon} tip={label ? undefined : tip} className="btn-danger-hover" onClick={() => setArmed(true)}>
        {label}
      </Button>
    );
  }
  return (
    <span
      className="confirm"
      role="group"
      aria-label={prompt}
      onKeyDown={(e) => {
        if (e.key === "Escape") setArmed(false);
      }}
    >
      <span className="confirm-prompt">{prompt}</span>
      <Button
        variant="danger"
        size="sm"
        loading={pending}
        onClick={() => {
          setArmed(false);
          onConfirm();
        }}
      >
        {confirmLabel}
      </Button>
      <Button ref={cancelRef} variant="ghost" size="sm" disabled={pending} onClick={() => setArmed(false)}>
        {t("common.cancel")}
      </Button>
    </span>
  );
}
