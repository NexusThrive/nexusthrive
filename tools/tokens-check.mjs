// Verifies that packages/brand/tokens/tokens.json mirrors packages/brand/css/tokens.css
// (brand, radius, shadow, space, z-index and motion tokens) and that the radius scale is
// the same in theme-v4.css and in the tokens documentation. Exits with 1 and lists every
// difference. No dependencies.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const read = (path) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");

const tokensCss = read("../packages/brand/css/tokens.css");
const themeCss = read("../packages/brand/css/theme-v4.css");
const tokensJson = JSON.parse(read("../packages/brand/tokens/tokens.json"));
const tokensDoc = read("../docs/06-reglas/sistema-de-diseno/tokens.md");

const errors = [];

// Declarations of a stylesheet, comments removed. The first declaration of a name wins:
// the light-mode `:root` block comes before the dark overrides.
function declarations(css) {
  const map = new Map();
  for (const [, name, value] of css.replace(/\/\*[\s\S]*?\*\//g, "").matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    if (!map.has(name)) map.set(name, value.trim().replace(/\s+/g, " "));
  }
  return map;
}

const css = declarations(tokensCss);
// theme-v4.css names tokens whose values live in tokens.css, so it resolves against both.
const theme = new Map([...css, ...declarations(themeCss)]);
const themeNames = [...declarations(themeCss).keys()];

// Follows `var(--other)` chains until a literal value.
function resolve(map, name) {
  let value = map.get(name);
  for (let hops = 0; value !== undefined && hops < 10; hops += 1) {
    const reference = value.match(/^var\((--[\w-]+)\)$/);
    if (!reference) return value.toLowerCase();
    value = map.get(reference[1]);
  }
  return value?.toLowerCase();
}

const at = (path) => path.split(".").reduce((node, key) => node?.[key], tokensJson);

// [path in tokens.json, name in tokens.css]
const mapping = [];
const add = (path, name) => mapping.push([path, name]);

for (const key of ["ink", "white", "paper"]) add(`color.${key}`, `--nx-${key}`);
add("color.inkSoft", "--nx-ink-soft");
for (const [group, shades] of Object.entries(tokensJson.color)) {
  if (typeof shades === "object" && group !== "status") {
    for (const shade of Object.keys(shades)) add(`color.${group}.${shade}`, `--nx-${group}-${shade}`);
  }
}
for (const key of Object.keys(tokensJson.color.status)) add(`color.status.${key}`, `--nx-${key}`);

const radiusNames = { none: "--radius-none", widget: "--radius-widget", full: "--radius-full" };
for (const key of Object.keys(tokensJson.radius)) add(`radius.${key}`, radiusNames[key] ?? `--nx-radius-${key}`);

for (const key of Object.keys(tokensJson.shadow)) add(`shadow.${key}`, `--nx-shadow-${key}`);

const motionNames = {
  fast: "--motion-fast",
  base: "--motion-base",
  ease: "--ease",
  d1: "--d1",
  d2: "--d2",
  d3: "--d3",
  d4: "--d4",
  overlayIn: "--motion-overlay-in",
  overlayOut: "--motion-overlay-out",
  popover: "--motion-popover",
  view: "--motion-view",
  easeOut: "--nx-ease-out",
  easeIn: "--nx-ease-in",
  easeInOut: "--nx-ease-in-out",
  easeSpring: "--nx-ease-spring",
  easeBounce: "--nx-ease-bounce",
};
for (const key of Object.keys(tokensJson.motion)) add(`motion.${key}`, motionNames[key] ?? `(no CSS name known for motion.${key})`);

const kebab = (key) => key.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`);
for (const key of Object.keys(tokensJson.zIndex)) add(`zIndex.${key}`, `--z-${kebab(key)}`);

for (const [path, name] of mapping) {
  const expected = String(at(path));
  const actual = resolve(css, name);
  if (actual === undefined) errors.push(`${name} is missing in tokens.css (tokens.json ${path} = ${expected})`);
  else if (actual !== expected.toLowerCase()) errors.push(`${name} is "${actual}" in tokens.css but "${expected}" in tokens.json (${path})`);
}

// Spacing: tokens.json lists the pixel values; tokens.css names them --space-<n> with n x 4px.
const cssSpace = [...css].filter(([name]) => /^--space-\d+$/.test(name)).map(([, value]) => parseInt(value, 10));
const jsonSpace = tokensJson.space.scale;
if (JSON.stringify(cssSpace) !== JSON.stringify(jsonSpace)) {
  errors.push(`space scale differs: tokens.css [${cssSpace}] vs tokens.json [${jsonSpace}]`);
}

// Reverse direction: a token declared in tokens.css for these groups must be in tokens.json.
const known = new Set(mapping.map(([, name]) => name));
const mirrored = /^(--nx-radius-|--radius-(none|widget|full)$|--nx-shadow-|--z-|--d\d$|--ease$|--nx-ease-|--motion-)/;
for (const [name, value] of css) {
  if (!mirrored.test(name) || known.has(name) || /^--radius-(xs|sm|md|lg|xl|2xl|3xl|sheet)$/.test(name)) continue;
  // Aliases (`--motion-feedback: var(--d1)`) name an existing value; only literals need a mirror.
  if (value.startsWith("var(")) continue;
  errors.push(`${name} exists in tokens.css but not in tokens.json`);
}

// Radius scale: theme-v4.css and the documentation must carry the same names and values.
const radiusKeys = Object.keys(tokensJson.radius);
for (const key of radiusKeys) {
  const expected = String(tokensJson.radius[key]).toLowerCase();
  const actual = resolve(theme, `--radius-${key}`);
  if (actual !== expected) errors.push(`--radius-${key} is "${actual}" in theme-v4.css but "${expected}" in tokens.json`);
}
for (const name of themeNames) {
  const key = name.match(/^--radius-(.+)$/)?.[1];
  if (key && !radiusKeys.includes(key)) errors.push(`${name} exists in theme-v4.css but not in tokens.json`);
}

const radiusRow = tokensDoc.split("\n").find((line) => line.startsWith("| Radios"));
if (!radiusRow) {
  errors.push("tokens.md has no `| Radios` row");
} else {
  for (const key of radiusKeys) {
    const expected = tokensJson.radius[key];
    // "`sm` 8" or "`full` 9999"; `widget` is documented as an alias of `3xl`.
    const documented = radiusRow.match(new RegExp("`" + key + "`\\s+(\\d+|\\(alias de `(\\w+)`\\))"));
    if (!documented) errors.push(`tokens.md does not document radius "${key}"`);
    else if (documented[2] ? String(tokensJson.radius[documented[2]]) !== expected : `${documented[1]}px` !== expected && documented[1] !== expected) {
      errors.push(`tokens.md documents radius "${key}" as ${documented[1]}, tokens.json has ${expected}`);
    }
  }
}

if (errors.length > 0) {
  console.error(`Token mismatch (${errors.length}):`);
  for (const error of errors) console.error(`  - ${error}`);
  process.exit(1);
}
console.log(`tokens.json mirrors tokens.css (${mapping.length} tokens checked).`);
