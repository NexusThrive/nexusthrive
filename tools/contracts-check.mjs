// Compare to the PR base from the local Git history, including the first-contract case.
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const run = (command, args, options = {}) => execFileSync(command, args, { cwd: root, ...options });
const buf = `${root}tools/bin/buf`;
run(buf, ["lint", "contracts"], { stdio: "inherit" });
const base = process.env.CONTRACT_BASE_REF || "main";
const files = run("git", ["ls-tree", "-r", "--name-only", base, "--", "contracts/"]).toString().trim().split("\n").filter(Boolean);
if (!files.some((file) => file.endsWith(".proto"))) {
  console.log(`Initial contract: ${base} has no Protobuf baseline; lint and generation remain required.`);
} else {
  const temp = mkdtempSync(join(tmpdir(), "contracts-base-"));
  try {
    for (const file of files) {
      const destination = join(temp, file);
      mkdirSync(dirname(destination), { recursive: true });
      writeFileSync(destination, run("git", ["show", `${base}:${file}`]));
    }
    run(buf, ["breaking", "contracts", "--against", join(temp, "contracts")], { stdio: "inherit" });
  } finally {
    rmSync(temp, { recursive: true, force: true });
  }
}
