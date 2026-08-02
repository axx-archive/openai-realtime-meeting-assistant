#!/usr/bin/env bash

set -Eeuo pipefail
umask 077
HOST=${HOST:-thebonfire.xyz}
MODE=${1:-}
B=${2:-}

case "$MODE" in
  blocked)
    if curl -fsS --noproxy '*' --connect-timeout 5 --max-time 8 "https://$HOST/healthz" >/dev/null; then
      printf 'FAIL: public app ingress is still reachable\n' >&2
      exit 1
    fi
    # TCP TURN must also be unreachable. UDP proof remains the VPS firewall
    # rule inspection plus room quiescence because a silent UDP service does
    # not offer a portable success/failure handshake here.
    if nc -z -w 5 "$HOST" 3478 >/dev/null 2>&1; then
      printf 'FAIL: public TCP TURN ingress is still reachable\n' >&2
      exit 1
    fi
    printf 'PASS: public HTTPS and TCP TURN are blocked from this Mac/network\n'
    ;;
  open)
    [[ $B =~ ^[0-9a-f]{40}$ ]] || { printf 'open mode requires exact B SHA\n' >&2; exit 2; }
    health=$(curl -fsS --noproxy '*' --connect-timeout 5 --max-time 20 "https://$HOST/healthz")
    ready=$(curl -fsS --noproxy '*' --connect-timeout 5 --max-time 20 "https://$HOST/readyz")
    jq -e --arg b "$B" '.ok==true and .version==$b and .release.releaseCommit==$b' <<<"$health" >/dev/null
    jq -e --arg b "$B" '.ok==true and .version==$b and .release.releaseCommit==$b' <<<"$ready" >/dev/null
    nc -z -w 5 "$HOST" 3478
    printf 'PASS: public exact B health/readiness and TCP TURN are reachable\n'
    ;;
  legacy)
    curl -fsS --noproxy '*' --connect-timeout 5 --max-time 20 "https://$HOST/healthz" | jq -e '.ok==true' >/dev/null
    curl -fsS --noproxy '*' --connect-timeout 5 --max-time 20 "https://$HOST/readyz" | jq -e '.ok==true' >/dev/null
    nc -z -w 5 "$HOST" 3478
    printf 'PASS: restored legacy health/readiness and TCP TURN are reachable\n'
    ;;
  *)
    printf 'Usage: mac-public-probe.sh blocked | open <B-SHA> | legacy\n' >&2
    exit 2
    ;;
esac
