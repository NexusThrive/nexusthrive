import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

test("exchange event payload accepts UUID v7 references and rejects malformed facts", async () => {
  const schema = JSON.parse(readFileSync(new URL("../contracts/events/kernel.exchange.recorded.v1.json", import.meta.url)));
  const { default: Ajv } = await import("ajv/dist/2020.js");
  const validate = new Ajv({ strict: true }).compile(schema);
  const valid = { exchange_id: "019a8000-0000-7000-8000-000000000001", commitment_id: "019a8000-0000-7000-8000-000000000002" };
  assert.equal(validate(valid), true);
  for (const invalid of [{}, { exchange_id: valid.exchange_id }, { ...valid, exchange_id: "invalid" }, { ...valid, actor_id: "asserted" }, { ...valid, commitment_id: 1 }]) {
    assert.equal(validate(invalid), false, JSON.stringify(invalid));
  }
});
