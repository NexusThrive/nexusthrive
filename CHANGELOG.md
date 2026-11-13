# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Foundation documentation: identity, universal model, architecture, distributions, integrations, ADRs, rules and engineering standards.
- Monorepo skeleton: `kernel`, `platform`, `distributions/business`, `services`, `apps`, `packages`, `contracts`, `integrations`, `infra` and `tools`.
- Data model for the `kernel` and `platform` schemas, and the stage 2 `business` schema.
- NexusThrive console design.
- Brand package (`packages/brand`): design tokens for light and dark mode, base CSS, Tailwind v4 theme, logo and the Public Stunt font with its license record.
- Design system rules in `docs/06-reglas/sistema-de-diseno`: brand, tone, tokens, interaction, motion, states, accessibility and UI quality checklist. ADR-0016.
- `@nexusthrive/brand` workspace package: exports, `files` (fonts included), optional React peer, typecheck, lockfile and `tools/tokens-check.mjs` (`task tokens:check`) to keep `tokens.json`, `tokens.css`, `theme-v4.css` and the radius docs in sync.
- CI job `branch-name` that enforces the branch naming rule.

### Changed

- The soft radius scale is the canonical one (ADR-0016); `none`, `widget` and `full` are now named in `tokens.css`, `theme-v4.css` and the tokens documentation. Straight corners apply to the logo only.
- Branch names are `feature|fix|chore|hotfix/HU-<slug>-<nn>`, or `sprint/S<nn>`; docs, `AGENTS.md`, `CONTRIBUTING.md` and the PR template state it with valid and invalid examples.
- The TypeScript CI job no longer uses `hashFiles` in its job condition, which GitHub Actions does not allow.
