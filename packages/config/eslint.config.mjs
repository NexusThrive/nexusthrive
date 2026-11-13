import js from "@eslint/js";
import tseslint from "typescript-eslint";

// Shared ESLint preset. `any` is banned outside generated code, which lives
// in `gen/` folders (see docs/07-ingenieria/code-quality.md).
export default tseslint.config(
  { ignores: ["**/dist/**", "**/gen/**", "**/node_modules/**"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  { rules: { "@typescript-eslint/no-explicit-any": "error" } },
);
