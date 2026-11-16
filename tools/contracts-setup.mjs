// Prepare pinned executables once; generation itself never downloads tools.
import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

const bin = fileURLToPath(new URL("./bin/", import.meta.url));
mkdirSync(bin, { recursive: true });
for (const tool of [
  "github.com/bufbuild/buf/cmd/buf@v1.50.0",
  "google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.5",
  "connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.18.1",
]) {
  execFileSync("go", ["install", tool], { env: { ...process.env, GOBIN: bin }, stdio: "inherit" });
}
