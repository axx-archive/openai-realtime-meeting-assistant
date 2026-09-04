#!/usr/bin/env bash
set -euo pipefail
pg_bin="${BUSINESS_PG_BIN:-/opt/homebrew/bin}"
http_test_root="$(mktemp -d /tmp/stride-business-http.XXXXXX)"
cleanup() {
 "$pg_bin/pg_ctl" -D "$http_test_root/data" -m immediate -w stop >/dev/null 2>&1 || true
 rm -rf "$http_test_root"
}
trap cleanup EXIT
mkdir "$http_test_root/socket"
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
"$pg_bin/initdb" -D "$http_test_root/data" -A trust -U postgres >/dev/null
"$pg_bin/pg_ctl" -D "$http_test_root/data" -l "$http_test_root/postgres.log" -o "-k $http_test_root/socket -p $port -h ''" -w start >/dev/null
export BUSINESS_HTTP_TEST_DATABASE_URL="postgres://postgres@/postgres?host=$http_test_root/socket&port=$port"
cd "$(dirname "$0")/.."
go test -count=1 -v -run '^TestBusinessHTTPPostgresLifecycle$' -timeout 30m .
