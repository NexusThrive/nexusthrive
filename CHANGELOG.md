# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Foundation documentation: identity, universal model, architecture, distributions, integrations, ADRs, rules and engineering standards.
- Monorepo skeleton: `kernel`, `platform`, `distributions/business`, `services`, `apps`, `packages`, `contracts`, `integrations`, `infra` and `tools`.
- Data model for the `kernel` and `platform` schemas, and the stage 2 `business` schema.
- NexusThrive console design.
- Phase B1 scaffolding: kernel architecture test, per-module Go tasks with `staticcheck` and `buf`, `@nexusthrive/config` shared presets and a CI that runs only what changed.
