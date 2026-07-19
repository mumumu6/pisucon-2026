#!/usr/bin/env bash

fail() { printf 'error: %s\n' "$*" >&2; exit 1; }
info() { printf '==> %s\n' "$*"; }
has() { command -v "$1" >/dev/null 2>&1; }

load_config() {
  local start_dir inventory_json reporter config_json
  start_dir="$PWD"
  PROJECT_ROOT="$(git -C "$start_dir" rev-parse --show-toplevel 2>/dev/null || printf '%s' "$start_dir")"
  ANSIBLE_INVENTORY="${ANSIBLE_INVENTORY:-$TOOL_ROOT/ansible/inventory.yml}"
  [[ -f "$ANSIBLE_INVENTORY" ]] || fail "Ansible inventory not found: $ANSIBLE_INVENTORY"
  has ansible-inventory || fail "ansible-inventory not found; run make setup"
  has jq || fail "jq not found; run make setup"

  inventory_json="$(ansible-inventory --inventory "$ANSIBLE_INVENTORY" --list)"
  reporter="$(jq --exit-status --raw-output '.reporter.hosts | select(length == 1) | .[0]' <<< "$inventory_json")" ||
    fail "inventory must define exactly one reporter"
  config_json="$(jq --compact-output --arg host "$reporter" '._meta.hostvars[$host]' <<< "$inventory_json")"

  LOG_ROOT="$(jq --exit-status --raw-output '.local_log_root' <<< "$config_json")"
  GITHUB_REPOSITORY="$(jq --raw-output '.github_repository // ""' <<< "$config_json")"
  CREATE_GITHUB_ISSUE="$(jq --raw-output '.create_github_issue // true' <<< "$config_json")"
  FLEET_AUTO_PUBLISH="$(jq --raw-output '.fleet_auto_publish // false' <<< "$config_json")"
  [[ "$LOG_ROOT" == '~/'* ]] && LOG_ROOT="$HOME/${LOG_ROOT#\~/}"
  mkdir -p "$LOG_ROOT"
}

latest_session() {
  find "$LOG_ROOT" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' 2>/dev/null |
    sort -nr | head -1 | cut -d' ' -f2-
}

resolve_session() {
  local requested="${1:-}" result
  if [[ -n "$requested" ]]; then
    [[ "$requested" = /* ]] && result="$requested" || result="$LOG_ROOT/$requested"
  else
    result="$(latest_session)"
  fi
  [[ -n "$result" && -d "$result" ]] || fail "benchmark session not found"
  printf '%s\n' "$result"
}

usage() {
  cat <<'EOF'
Usage: isucon-bench <command>

  setup              install Ansible, jq, git and gh on this control machine
  fleet bootstrap    restore tools and Git state after server recreation
  fleet setup        install measurement tools on every inventory host
  fleet enable       start netdata and slow-query logging on the fleet
  fleet disable      stop all fleet-wide measurement overhead
  fleet instrument on|off
                     add or remove isolated Go pprof instrumentation on app hosts
  fleet bench        orchestrate, report and collect one benchmark run
  fleet collect [ID] fetch the latest or selected report to this machine
  publish [SESSION]  create a GitHub Issue from collected analysis results
EOF
}
