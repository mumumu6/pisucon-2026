#!/usr/bin/env bash

run_playbook() {
  local playbook="$1"
  has ansible-playbook || fail "ansible-playbook not found; run setup"
  [[ -f "$ANSIBLE_INVENTORY" ]] || fail "Ansible inventory not found: $ANSIBLE_INVENTORY"
  ansible-playbook --inventory "$ANSIBLE_INVENTORY" "$TOOL_ROOT/ansible/$playbook"
}

fleet_extra_vars() {
  local session_id="$1"
  jq --null-input --compact-output \
    --arg session_id "$session_id" \
    --arg requested_session "$session_id" \
    '{session_id: $session_id, requested_session: $requested_session}'
}

command_fleet_setup() { run_playbook setup.yml; }
command_fleet_bootstrap() {
  run_playbook setup.yml
  run_playbook git.yml
}
command_fleet_enable() { run_playbook enable.yml; }
command_fleet_disable() { run_playbook disable.yml; }

command_fleet_instrument() {
  local instrument_state="${1:-}" extra_vars
  [[ "$instrument_state" == on || "$instrument_state" == off ]] ||
    fail "usage: isucon-bench fleet instrument on|off"
  has jq || fail "jq not found; run setup"
  has ansible-playbook || fail "ansible-playbook not found; run setup"
  [[ -f "$ANSIBLE_INVENTORY" ]] || fail "Ansible inventory not found: $ANSIBLE_INVENTORY"

  extra_vars="$(jq --null-input --compact-output \
    --arg instrument_state "$instrument_state" \
    '{instrument_state: $instrument_state}')"
  ansible-playbook --inventory "$ANSIBLE_INVENTORY" \
    --extra-vars "$extra_vars" "$TOOL_ROOT/ansible/instrument.yml"
}

command_fleet_bench() {
  local publish_requested="${1:-}" session_id extra_vars benchmark_status=0
  has jq || fail "jq not found; run setup"
  has ansible-playbook || fail "ansible-playbook not found; run setup"
  [[ -f "$ANSIBLE_INVENTORY" ]] || fail "Ansible inventory not found: $ANSIBLE_INVENTORY"
  [[ -z "$publish_requested" || "$publish_requested" == --publish ]] ||
    fail "usage: isucon-bench fleet bench [--publish]"

  session_id="$(date +%Y%m%d-%H%M%S)"
  extra_vars="$(fleet_extra_vars "$session_id")"
  ansible-playbook --inventory "$ANSIBLE_INVENTORY" \
    --extra-vars "$extra_vars" "$TOOL_ROOT/ansible/bench.yml" || benchmark_status=$?
  ansible-playbook --inventory "$ANSIBLE_INVENTORY" \
    --extra-vars "$extra_vars" "$TOOL_ROOT/ansible/collect.yml"

  if (( benchmark_status != 0 )); then
    return "$benchmark_status"
  fi

  if [[ "$publish_requested" == --publish || "$FLEET_AUTO_PUBLISH" == true ]]; then
    command_publish "$session_id"
  fi
}

command_fleet_collect() {
  local requested_session="${1:-}" extra_vars
  has jq || fail "jq not found; run setup"
  extra_vars="$(jq --null-input --compact-output \
    --arg requested_session "$requested_session" \
    '{requested_session: $requested_session}')"
  has ansible-playbook || fail "ansible-playbook not found; run setup"
  [[ -f "$ANSIBLE_INVENTORY" ]] || fail "Ansible inventory not found: $ANSIBLE_INVENTORY"
  ansible-playbook --inventory "$ANSIBLE_INVENTORY" \
    --extra-vars "$extra_vars" "$TOOL_ROOT/ansible/collect.yml"
}

command_fleet_help() { fail "usage: isucon-bench fleet bootstrap|setup|enable|disable|instrument on|off|bench [--publish]|collect [SESSION]"; }
