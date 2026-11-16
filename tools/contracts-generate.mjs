// Local plugins and prepared npm dependencies make generation independent of BSR.
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
execFileSync(`${root}tools/bin/buf`, ["generate"], { cwd: `${root}contracts`, stdio: "inherit" });
