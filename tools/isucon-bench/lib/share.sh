#!/usr/bin/env bash

issue_comment() {
  local issue_url="$1" title="$2" source="$3" body
  [[ -s "$source" ]] || return 0
  body="$(mktemp)"
  {
    printf '## %s\n\n```text\n' "$title"
    # GitHub Issue本文にはサイズ制限があるため、Markdownの装飾分を残します。
    head -c 58000 "$source"
    if [[ "$(wc -c < "$source")" -gt 58000 ]]; then
      printf '\n... truncated; full output remains below local_log_root.\n'
    fi
    printf '\n```\n'
  } > "$body"
  gh issue comment "$issue_url" --body-file "$body"
  rm -f "$body"
}

command_publish() {
  local session report title issue_body issue_url artifact host section
  session="$(resolve_session "$1")"
  report="$session/REPORT.md"
  [[ -f "$report" ]] || fail "report is missing; run bench or collect first"
  if [[ "$CREATE_GITHUB_ISSUE" == true ]] && has gh; then
    title="ISUCON benchmark $(basename "$session")"
    issue_body="$(mktemp)"
    head -c 58000 "$report" > "$issue_body"
    if [[ "$(wc -c < "$report")" -gt 58000 ]]; then
      printf '\n\n... truncated; the complete report remains below local_log_root.\n' >> "$issue_body"
    fi
    if [[ -n "$GITHUB_REPOSITORY" ]]; then
      issue_url="$(gh issue create --repo "$GITHUB_REPOSITORY" --title "$title" --body-file "$issue_body")"
    else
      issue_url="$(cd "$PROJECT_ROOT" && gh issue create --title "$title" --body-file "$issue_body")"
    fi
    rm -f "$issue_body"
    while IFS= read -r artifact; do
      host="$(basename "$(dirname "$artifact")")"
      section="$(basename "$artifact" .txt)"
      issue_comment "$issue_url" "$section: $host" "$artifact"
    done < <(find "$session" -mindepth 2 -maxdepth 2 -type f \
      \( -name 'alp.txt' -o -name 'pt-query-digest.txt' -o -name 'pprof.txt' -o -name 'netdata.txt' \) \
      | sort)
    info "GitHub Issue created: $issue_url"
  elif [[ "$CREATE_GITHUB_ISSUE" == true ]]; then
    fail "gh is not installed; run setup and gh auth login"
  else
    info "GitHub Issue creation is disabled; report remains at $report"
  fi
}
