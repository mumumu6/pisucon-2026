#!/usr/bin/env bash

command_setup() {
  has sudo || fail "sudo is required"
  has apt-get || fail "automatic setup currently supports Debian/Ubuntu"

  info "installing system packages"
  sudo apt-get update
  sudo apt-get install -y ansible gh git jq

  info "setup complete"
}
