import { LoaderCircle } from "lucide-react";

export function Spinner({
  size = 14,
  label,
}: {
  size?: number;
  label?: string;
}) {
  return (
    <>
      <LoaderCircle className="spin" size={size} aria-hidden />
      {label && <span className="sr-only">{label}</span>}
    </>
  );
}
