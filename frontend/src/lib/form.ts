// Wires a control to its <Field>: id plus the invalid/description ARIA pair.
export function control(id: string, error?: string | null) {
  return {
    id,
    "aria-invalid": error ? (true as const) : undefined,
    "aria-describedby": error ? `${id}-msg` : undefined,
  };
}

/** Show validation errors only once the user has tried to submit. */
export function shown<T extends Record<string, string | null>>(
  errors: T,
  submitted: boolean,
): T {
  if (submitted) return errors;
  return Object.fromEntries(Object.keys(errors).map((k) => [k, null])) as T;
}

export const hasErrors = (errors: Record<string, string | null>) =>
  Object.values(errors).some((e) => e !== null);

export function isHttpUrl(value: string): boolean {
  try {
    const u = new URL(value);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}
