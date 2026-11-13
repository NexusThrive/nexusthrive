# @nexusthrive/brand

Brand foundation for every NexusThrive surface: design tokens, base CSS, the Tailwind v4 theme, fonts and the logo.
The rules for using them are documented in Spanish in [`docs/06-reglas/sistema-de-diseno`](../../docs/06-reglas/sistema-de-diseno/README.md).

## Contents

| Path | What it is |
|---|---|
| `tokens/tokens.json` | Token values for design tools and non-CSS consumers |
| `css/tokens.css` | Token values as CSS variables, light and dark mode. Source of truth |
| `css/theme-v4.css` | Names the tokens for Tailwind v4 (`@theme inline`) |
| `css/base.css` | Base element styles: body, headings, focus, wordmark, brand motif |
| `css/fonts.css` | `@font-face` rules for Public Stunt |
| `fonts/public-stunt/` | Public Stunt files and their [license record](fonts/public-stunt/LICENSE.md) |
| `logo/` | Lockup and mark as SVG, in ink and white |
| `src/brand-logo.tsx` | React logo component (`lockup` or `mark`), painted with `currentColor` |

## Usage

Import the CSS in this order in the app stylesheet:

```css
@import "tailwindcss";
@import "@nexusthrive/brand/fonts.css";
@import "@nexusthrive/brand/tokens.css";
@import "@nexusthrive/brand/theme-v4.css";
@import "@nexusthrive/brand/base.css";
@custom-variant dark (&:where(.dark, .dark *));
```

Dark mode is the `.dark` class (or `data-theme="dark"`) on `<html>`. Compact density is `data-density="compact"`.

## Rules

- Components never write a raw color, shadow, z-index or duration. They use a token; if one is missing, add it here.
- `css/tokens.css` and `tokens/tokens.json` change together, in the same commit.
- Montserrat, Inter and JetBrains Mono are open fonts (SIL OFL 1.1). Each app self-hosts them when it is created.
- Public Stunt is for brand pieces only. Check its license record before serving it on the web or bundling it in an app.
- The logo is never redrawn, recolored outside ink and white, rotated or given effects.

## Status

This folder holds the assets. It becomes a workspace package (`package.json` with exports) when the first app needs it,
together with the lockfile. Components built on these tokens live in [`@nexusthrive/ui`](../ui/README.md).
