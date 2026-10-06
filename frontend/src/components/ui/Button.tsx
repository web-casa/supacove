import type { AnchorHTMLAttributes, ButtonHTMLAttributes, Ref } from "react";
import type { LucideIcon } from "lucide-react";
import { Spinner } from "./Spinner";

type Variant = "primary" | "secondary" | "ghost" | "danger";

interface Common {
  variant?: Variant;
  size?: "sm" | "md";
  icon?: LucideIcon;
  /** Icon-only button: `tip` becomes the accessible name and hover label. */
  tip?: string;
  block?: boolean;
}

function classes(
  { variant = "secondary", size = "md", block }: Common,
  iconOnly: boolean,
  extra?: string,
) {
  return [
    "btn",
    `btn-${variant}`,
    `btn-${size}`,
    iconOnly && "btn-icon",
    block && "btn-block",
    extra,
  ]
    .filter(Boolean)
    .join(" ");
}

type ButtonProps = Common &
  ButtonHTMLAttributes<HTMLButtonElement> & {
    loading?: boolean;
    ref?: Ref<HTMLButtonElement>;
  };

export function Button({
  variant,
  size,
  icon: Icon,
  tip,
  block,
  loading,
  disabled,
  className,
  children,
  type = "button",
  ...rest
}: ButtonProps) {
  const iconSize = size === "sm" ? 13 : 15;
  return (
    <button
      type={type}
      className={classes({ variant, size, block }, !children, className)}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      aria-label={tip}
      data-tip={tip}
      {...rest}
    >
      {loading ? (
        <Spinner size={iconSize} />
      ) : (
        Icon && <Icon size={iconSize} aria-hidden />
      )}
      {children}
    </button>
  );
}

type LinkProps = Common & AnchorHTMLAttributes<HTMLAnchorElement>;

/** Same visuals as Button for plain navigations (cookie-authenticated downloads). */
export function LinkButton({
  variant,
  size,
  icon: Icon,
  tip,
  block,
  className,
  children,
  ...rest
}: LinkProps) {
  return (
    <a
      className={classes({ variant, size, block }, !children, className)}
      aria-label={tip}
      data-tip={tip}
      {...rest}
    >
      {Icon && <Icon size={size === "sm" ? 13 : 15} aria-hidden />}
      {children}
    </a>
  );
}
