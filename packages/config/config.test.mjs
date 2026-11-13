import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { URL } from "node:url";

test("tsconfig base enables strict mode", () => {
  const base = JSON.parse(readFileSync(new URL("./tsconfig.base.json", import.meta.url), "utf8"));
  assert.equal(base.compilerOptions.strict, true);
});

test("eslint preset bans explicit any", async () => {
  const { default: preset } = await import("./eslint.config.mjs");
  const rules = preset.map((entry) => entry.rules?.["@typescript-eslint/no-explicit-any"]);
  assert.ok(rules.includes("error"));
});
