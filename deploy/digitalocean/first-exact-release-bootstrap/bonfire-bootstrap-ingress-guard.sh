#!/usr/bin/env bash

set -Eeuo pipefail
umask 077
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

CHAIN=BONFIRE_BOOTSTRAP_RAW
WAN_INTERFACE=eth0

verify_family() {
  local tool=$1
  "$tool" -t mangle -C PREROUTING -i "$WAN_INTERFACE" -j "$CHAIN"
  test "$("$tool" -t mangle -S PREROUTING | grep -F -- "-A PREROUTING -i $WAN_INTERFACE -j $CHAIN" | wc -l)" -eq 1
  "$tool" -t mangle -C "$CHAIN" -p tcp -m multiport --dports 80,443,3478 -j DROP
  "$tool" -t mangle -C "$CHAIN" -p udp --dport 3478 -j DROP
  "$tool" -t mangle -C "$CHAIN" -p udp --dport 40000:40100 -j DROP
  "$tool" -t mangle -C "$CHAIN" -p udp --dport 49160:49200 -j DROP
  "$tool" -t mangle -C "$CHAIN" -j RETURN
  test "$("$tool" -t mangle -S "$CHAIN" | wc -l)" -eq 6
}

apply_family() {
  local tool=$1
  while "$tool" -t mangle -C PREROUTING -i "$WAN_INTERFACE" -j "$CHAIN" >/dev/null 2>&1; do
    "$tool" -t mangle -D PREROUTING -i "$WAN_INTERFACE" -j "$CHAIN"
  done
  if "$tool" -t mangle -S "$CHAIN" >/dev/null 2>&1; then
    "$tool" -t mangle -F "$CHAIN"
  else
    "$tool" -t mangle -N "$CHAIN"
  fi
  "$tool" -t mangle -A "$CHAIN" -p tcp -m multiport --dports 80,443,3478 -j DROP
  "$tool" -t mangle -A "$CHAIN" -p udp --dport 3478 -j DROP
  "$tool" -t mangle -A "$CHAIN" -p udp --dport 40000:40100 -j DROP
  "$tool" -t mangle -A "$CHAIN" -p udp --dport 49160:49200 -j DROP
  "$tool" -t mangle -A "$CHAIN" -j RETURN
  "$tool" -t mangle -I PREROUTING 1 -i "$WAN_INTERFACE" -j "$CHAIN"
  verify_family "$tool"
}

remove_family() {
  local tool=$1
  while "$tool" -t mangle -C PREROUTING -i "$WAN_INTERFACE" -j "$CHAIN" >/dev/null 2>&1; do
    "$tool" -t mangle -D PREROUTING -i "$WAN_INTERFACE" -j "$CHAIN"
  done
  if "$tool" -t mangle -S "$CHAIN" >/dev/null 2>&1; then
    "$tool" -t mangle -F "$CHAIN"
    "$tool" -t mangle -X "$CHAIN"
  fi
}

case ${1:-} in
  apply)
    apply_family iptables
    apply_family ip6tables
    ;;
  remove)
    remove_family iptables
    remove_family ip6tables
    ;;
  status)
    verify_family iptables
    verify_family ip6tables
    ;;
  *)
    printf '%s\n' 'Usage: bonfire-bootstrap-ingress-guard.sh apply|remove|status' >&2
    exit 2
    ;;
esac
