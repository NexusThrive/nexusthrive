// Renders every Mermaid block found in docs/ with the Mermaid CLI and fails
// on the first syntax error. Requires `npx @mermaid-js/mermaid-cli`.
import { execFileSync } from "node:child_process";
import { mkdtempSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const root = new URL("../docs/", import.meta.url).pathname;
const out = mkdtempSync(join(tmpdir(), "docs-check-"));

function markdownFiles(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return markdownFiles(path);
    return name.endsWith(".md") ? [path] : [];
  });
}

let count = 0;
for (const file of markdownFiles(root)) {
  const blocks = readFileSync(file, "utf8").matchAll(/```mermaid\n([\s\S]*?)```/g);
  for (const [, source] of blocks) {
    count += 1;
    const input = join(out, `${count}.mmd`);
    writeFileSync(input, source);
    try {
      execFileSync("npx", ["-y", "@mermaid-js/mermaid-cli", "-i", input, "-o", `${input}.svg`], { stdio: "pipe" });
    } catch (error) {
      console.error(`Invalid diagram in ${file}`);
      console.error(String(error.stderr));
      process.exit(1);
    }
  }
}
console.log(`${count} diagrams rendered successfully.`);
