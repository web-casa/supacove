import { describe, expect, it } from "vitest";
import { analyze, applyPatch, buildUri, detectProvider, isLocalHost } from "./connection";

describe("provider detection", () => {
  it("classifies hosts by suffix only", () => {
    expect(detectProvider("db.abc.supabase.co")).toBe("supabase");
    expect(detectProvider("ep-x.eu.neon.tech")).toBe("neon");
    expect(detectProvider("a.proxy.rlwy.net")).toBe("railway");
    expect(detectProvider("db.internal")).toBe("generic");
    expect(detectProvider("pooler.supabase.com.evil.invalid")).toBe("generic");
  });
  it("loopback forms", () => {
    expect(isLocalHost("localhost")).toBe(true);
    expect(isLocalHost("127.0.0.5")).toBe(true);
    expect(isLocalHost("::1")).toBe(true);
    expect(isLocalHost("10.0.0.1")).toBe(false);
  });
});

describe("analyze", () => {
  it("blocks non-postgres schemes", () => {
    expect(analyze("mysql://u:p@h/db", "require").blocked).toBe(true);
  });
  it("masks the password in the display copy, keeps it in normalized", () => {
    const a = analyze("postgresql://u:s3cret@db.internal:5432/app", "require");
    expect(a.redacted).toContain("••••••");
    expect(a.redacted).not.toContain("s3cret");
    expect(a.normalized).toContain("s3cret");
  });
  it("placeholder passwords are an error", () => {
    const a = analyze("postgresql://u:[YOUR-PASSWORD]@db.internal/app", "require");
    expect(a.blocked).toBe(true);
  });
  it("supabase transaction pooler port warns with a fix", () => {
    const a = analyze("postgresql://u:p@pooler.ref.supabase.com:6543/postgres", "require");
    const fix = a.findings.find((f) => f.fix);
    expect(fix?.fix?.patch.port).toBe("5432");
  });
  it("neon -pooler host warns on any port", () => {
    const a = analyze("postgresql://u:p@ep-x-pooler.eu.aws.neon.tech/db", "require");
    expect(a.findings.some((f) => f.level === "warn")).toBe(true);
  });
  it("drops unsupported params and says so", () => {
    const a = analyze("postgresql://u:p@h/db?channel_binding=require&sslmode=require", "require");
    expect(a.normalized).not.toContain("channel_binding");
    expect(a.findings.some((f) => f.key === "conn.info.droppedParams")).toBe(true);
  });
  it("adds the chosen sslmode when absent", () => {
    const a = analyze("postgresql://u:p@h/db", "verify-full");
    expect(a.normalized).toContain("sslmode=verify-full");
  });
  it("disable on a remote host warns", () => {
    const a = analyze("postgresql://u:p@h/db?sslmode=disable", "disable");
    expect(a.findings.some((f) => f.key === "conn.warn.sslDisable")).toBe(true);
  });
  it("oversized strings are blocked", () => {
    const long = "postgresql://u:p@h/" + "d".repeat(600);
    expect(analyze(long, "require").blocked).toBe(true);
  });
});

describe("buildUri encodes special characters", () => {
  it("password with : / @ # survives a round trip", () => {
    const uri = buildUri(
      { host: "db.internal", port: "5432", database: "app db", user: "u s", password: "a:b/c@d#e" },
      "require",
    );
    expect(uri).toContain("sslmode=require");
    expect(uri).not.toContain("a:b/c@d#e"); // encoded, never raw
    const a = analyze(uri, "require");
    expect(a.blocked).toBe(false);
  });
});

describe("applyPatch one-click fixes", () => {
  it("replaces the port without touching credentials", () => {
    const fixed = applyPatch("postgresql://u:s3cret@pooler.ref.supabase.com:6543/postgres", { port: "5432" });
    expect(fixed).toContain(":5432/");
    expect(fixed).toContain("s3cret");
  });
});
