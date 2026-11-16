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

test("expiry event payload keeps exact decimal strings and complete references", async () => {
  const schema = JSON.parse(readFileSync(new URL("../contracts/events/kernel.commitment.expired.v1.json", import.meta.url)));
  const { default: Ajv } = await import("ajv/dist/2020.js");
  const validate = new Ajv({ strict: true }).compile(schema);
  const id = "019a8000-0000-7000-8000-000000000001";
  const valid = { commitment_id: id, timer_id: id, kind: "test.commitment", debtor_participant_id: id, creditor_participant_id: id, resource_id: id, amount: "100.25", fulfilled_amount: "0", due_at: "2026-11-15T00:00:00Z", expired_at: "2026-11-16T00:00:00Z" };
  assert.equal(validate(valid), true);
  for (const invalid of [{ ...valid, amount: 100.25 }, { ...valid, resource_id: "invalid" }, { ...valid, secret: "forbidden" }, { ...valid, due_at: "invalid" }]) {
    assert.equal(validate(invalid), false, JSON.stringify(invalid));
  }
});
