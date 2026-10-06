import { describe, expect, it } from "vitest";
import { en } from "./en";
import { zhCN } from "./zh-CN";
import { translate } from "./core";

const enKeys = Object.keys(en).sort();
const zhKeys = Object.keys(zhCN).sort();

describe("dictionary symmetry", () => {
  it("zh-CN defines exactly the English key set", () => {
    expect(zhKeys).toEqual(enKeys);
  });

  it("no empty translations", () => {
    for (const k of enKeys) {
      expect(en[k as keyof typeof en].length, `en ${k}`).toBeGreaterThan(0);
      expect(zhCN[k as keyof typeof zhCN].length, `zh ${k}`).toBeGreaterThan(0);
    }
  });

  it("interpolation variables match per key", () => {
    const vars = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
    for (const k of enKeys) {
      expect(vars(zhCN[k as keyof typeof zhCN]), `vars of ${k}`).toEqual(vars(en[k as keyof typeof en]));
    }
  });

  it("plural groups carry one/other on both sides", () => {
    const bases = new Set(enKeys.filter((k) => k.endsWith(".one")).map((k) => k.slice(0, -4)));
    for (const b of bases) {
      expect(enKeys).toContain(`${b}.other`);
      expect(zhKeys).toContain(`${b}.one`);
      expect(zhKeys).toContain(`${b}.other`);
    }
  });
});

describe("translate behavior", () => {
  it("selects language and interpolates", () => {
    expect(translate("zh-CN", "dbrow.limit", { n: 48 })).toBe(" · 阈值 48 小时");
    expect(translate("en", "dbrow.limit", { n: 48 })).toBe(" · limit 48h");
  });
  it("plural one/other by count", () => {
    expect(translate("en", "pipe.stage.remote.sub", { count: 1 })).toBe("1 destination");
    expect(translate("en", "pipe.stage.remote.sub", { count: 3 })).toBe("3 destinations");
  });
  it("unknown keys pass through without crashing", () => {
    expect(translate("en", "no.such.key")).toBe("no.such.key");
  });
  it("variable injection is inert text (no markup interpretation)", () => {
    const out = translate("en", "dbrow.removePrompt", { name: "<img src=x onerror=alert(1)>" });
    expect(out).toContain("<img src=x onerror=alert(1)>");
    expect(out).not.toContain("{name}");
  });
});
