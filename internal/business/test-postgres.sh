#!/usr/bin/env bash
set -euo pipefail
# Only this temporary cluster is created/stopped. No existing database is used.
pg_bin="${BUSINESS_PG_BIN:-/opt/homebrew/bin}"
test_root="$(mktemp -d /tmp/stride-business-test.XXXXXX)"
cleanup() {
  "$pg_bin/pg_ctl" -D "$test_root/data" -m immediate -w stop >/dev/null 2>&1 || true
  rm -rf "$test_root"
}
trap cleanup EXIT
mkdir "$test_root/socket"
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
"$pg_bin/initdb" -D "$test_root/data" -A trust -U postgres >/dev/null
"$pg_bin/pg_ctl" -D "$test_root/data" -l "$test_root/postgres.log" -o "-k $test_root/socket -p $port -h ''" -w start >/dev/null
export BUSINESS_TEST_DATABASE_URL="postgres://postgres@/postgres?host=$test_root/socket&port=$port"
cd "$(dirname "$0")/../.."
go test -race -count=1 -v ./internal/business
