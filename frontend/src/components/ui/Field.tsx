import type { ReactNode } from "react";
import { CircleAlert } from "lucide-react";

interface Props {
  label: string;
  htmlFor: string;
  hint?: ReactNode;
  error?: string | null;
  children: ReactNode;
}

/** Label + control + hint, with the validation error rendered in place.
    The control should set aria-invalid and aria-describedby={`${htmlFor}-msg`}. */
export function Field({ label, htmlFor, hint, error, children }: Props) {
  return (
    <div className={`field${error ? " field-invalid" : ""}`}>
      <label htmlFor={htmlFor}>{label}</label>
      {children}
      {error ? (
        <p className="field-msg field-error" id={`${htmlFor}-msg`} role="alert">
          <CircleAlert size={12} aria-hidden />
          {error}
        </p>
      ) : (
        hint && (
          <p className="field-msg" id={`${htmlFor}-msg`}>
            {hint}
          </p>
        )
      )}
    </div>
  );
}
