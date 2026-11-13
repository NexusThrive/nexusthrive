# Contributing to NexusThrive

Thank you for contributing. This guide summarizes how work is done in this repository. The full engineering standard lives in `docs/07-ingenieria/` (Spanish).

## Before you start

1. Read `AGENTS.md` and `docs/02-arquitectura/hoja-de-ruta.md`.
2. Make sure the work belongs to the current phase.
3. If the change involves an architectural decision, write an ADR first using `docs/05-decisiones/plantilla.md`.

## Workflow

1. Create a branch from `main` that matches `^(feature|fix|chore|hotfix)/HU-[a-z0-9]+(-[a-z0-9]+)*-[0-9]{2}$`.
   - Valid: `feature/HU-kernel-ledger-01`, `fix/HU-outbox-retry-02`, `chore/HU-scaffolding-01`. Sprint integration branches are `sprint/S<nn>`.
   - Invalid: `feat/HU-kernel-ledger-01`, `docs/HU-platform-console-01`, `feature/kernel-ledger`, `feature/HU-kernel-ledger-1`.
   - The branch prefix is not the commit type: a `feature/` branch can hold `feat`, `fix`, `docs` or `chore` commits.
2. Write tests first, then the implementation.
3. Commit with Conventional Commits in English: `type(scope): summary`.
4. Update `docs/` (Spanish) in the same change.
5. Run `task lint`, `task test` and `task docs:check`.
6. Open a pull request using the template. Title and description in English.
7. Merge with squash once CI is green and the review is approved.

## Language policy

- Documentation in `docs/`: Spanish.
- Code, comments, logs, tests, commits, pull requests, issues and branches: English.
- User-facing text: keys in `@nexusthrive/i18n`, never literals in code.

## Code quality

- Comments explain purpose, constraints and reasons, right above the element.
- No dead or commented-out code.
- Money with exact decimals; never floating point.
- No business vocabulary inside `kernel/`.

## Commit authorship

Commits carry the identity of the responsible developer. Do not add `Co-Authored-By` trailers, signatures or mentions of tools or assistants.

## Reporting issues

Use the issue templates in `.github/ISSUE_TEMPLATE/`. Write issues in English.
