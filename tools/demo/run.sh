#!/usr/bin/env bash
# Starts the local kernel demo: a database of its own on the project PostgreSQL
# (task infra:up, port 55432), the migrations, a seeded space and the API with
# the development console at http://127.0.0.1:18090/. Development only.
set -euo pipefail
cd "$(dirname "$0")/../.."
base="postgres://nexusthrive:nexusthrive@localhost:55432"
docker compose -f infra/docker/compose.yaml exec -T postgres psql -U nexusthrive -d postgres -tc \
  "SELECT 1 FROM pg_database WHERE datname = 'nexusthrive_demo'" | grep -q 1 \
  || docker compose -f infra/docker/compose.yaml exec -T postgres psql -U nexusthrive -d postgres -c "CREATE DATABASE nexusthrive_demo"
export DATABASE_URL="$base/nexusthrive_demo?sslmode=disable"
go run ./kernel/db/cmd/migrate
set -a
eval "$(go run ./kernel/state/cmd/seed)"
set +a
export NEXUSTHRIVE_DEV=1 ADDR="${ADDR:-127.0.0.1:18090}"
exec go run ./services/api/cmd/api
