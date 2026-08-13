#!/usr/bin/env bash

set -euo pipefail

readonly bundle_id="com.anaregdesign.lanternExample"
readonly flutter_bin="${IOS_SMOKE_FLUTTER_BIN:-flutter}"
readonly xcrun_bin="${IOS_SMOKE_XCRUN_BIN:-xcrun}"
readonly ps_bin="${IOS_SMOKE_PS_BIN:-ps}"
readonly launch_timeout="${IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS:-90}"
readonly total_timeout="${IOS_SMOKE_TOTAL_TIMEOUT_SECONDS:-480}"
readonly poll_interval="${IOS_SMOKE_POLL_INTERVAL_SECONDS:-1}"
readonly diagnostic_timeout="${IOS_SMOKE_DIAGNOSTIC_TIMEOUT_SECONDS:-10}"
readonly max_diagnostic_bytes=262144

require_positive_integer() {
  local name=$1 value=$2
  if [[ ! "$value" =~ ^[1-9][0-9]*$ ]]; then
    echo "invalid $name: $value" >&2
    exit 64
  fi
}

redact_text() {
  local source=$1 destination=$2
  sed -E \
    -e 's#https?://[^[:space:]<>]+#<redacted-url>#g' \
    -e 's#(Authorization:?[[:space:]]*(Bearer[[:space:]]*)?)[^[:space:]]+#\1<redacted>#Ig' \
    -e 's#((LANTERN_)?TOKEN|token)[=:][^[:space:]]+#\1=<redacted>#g' \
    -e 's#mobile-smoke:[^[:space:]]+#mobile-smoke:<redacted>#g' \
    -e 's#[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}#<redacted-jwt>#g' \
    "$source" > "$destination"
}

capture_bounded() {
  local destination=$1
  shift
  local raw
  raw=$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/lantern-ios-diagnostic.XXXXXX")
  local pid polls=0 status=0
  local max_polls=$((diagnostic_timeout * 10))
  "$@" > "$raw" 2>&1 &
  pid=$!
  while kill -0 "$pid" 2>/dev/null; do
    if (( polls >= max_polls )); then
      kill -TERM "$pid" 2>/dev/null || true
      sleep 1
      kill -KILL "$pid" 2>/dev/null || true
      break
    fi
    sleep 0.1
    polls=$((polls + 1))
  done
  wait "$pid" || status=$?
  LC_ALL=C head -c "$max_diagnostic_bytes" "$raw" > "${raw}.bounded"
  redact_text "${raw}.bounded" "$destination"
  rm -f "$raw" "${raw}.bounded"
  return "$status"
}

capture_diagnostics() {
  local device=$1 destination=$2
  mkdir -p "$destination"
  capture_bounded "$destination/simctl-devices.txt" \
    "$xcrun_bin" simctl list devices || true
  capture_bounded "$destination/installed-apps.txt" \
    "$xcrun_bin" simctl listapps "$device" || true
  capture_bounded "$destination/app-container.txt" \
    "$xcrun_bin" simctl get_app_container "$device" "$bundle_id" app || true
  capture_bounded "$destination/process-tree.txt" \
    "$ps_bin" -axo pid,ppid,state,etime,command || true
  capture_bounded "$destination/simulator.log" \
    "$xcrun_bin" simctl spawn "$device" log show --last 5m --style compact \
    --predicate 'process == "Runner" OR process == "SpringBoard" OR subsystem BEGINSWITH "com.apple.FrontBoard"' || true
}

descendants_of() {
  local parent=$1 child
  for child in $(pgrep -P "$parent" 2>/dev/null || true); do
    descendants_of "$child"
    echo "$child"
  done
}

terminate_process_tree() {
  local root=$1 child
  local descendants=""
  descendants=$(descendants_of "$root")
  for child in $descendants; do
    kill -TERM "$child" 2>/dev/null || true
  done
  kill -TERM "$root" 2>/dev/null || true
  sleep 2
  for child in $descendants; do
    kill -KILL "$child" 2>/dev/null || true
  done
  kill -KILL "$root" 2>/dev/null || true
}

record_classification() {
  local classification=$1 destination=$2
  printf '%s\n' "$classification" > "$destination/classification.txt"
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    printf 'classification=%s\n' "$classification" >> "$GITHUB_OUTPUT"
  fi
}

sanitize_bounded_file() {
  local source=$1 bounded sanitized
  bounded=$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/lantern-ios-bounded.XXXXXX")
  sanitized=$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/lantern-ios-sanitized.XXXXXX")
  LC_ALL=C tail -c "$max_diagnostic_bytes" "$source" > "$bounded"
  redact_text "$bounded" "$sanitized"
  mv "$sanitized" "$source"
  rm -f "$bounded" "$sanitized"
}

finalize_diagnostics() {
  local root=$1 file size finalized
  local count=0 total=0
  mkdir -p "$root"
  if find "$root" -mindepth 1 ! -type d ! -type f -print -quit | grep -q .; then
    echo "diagnostics contain a non-regular entry" >&2
    return 1
  fi
  while IFS= read -r -d '' file; do
    case "$file" in
      *.raw | *.bounded | *.sanitized)
        rm -f "$file"
        continue
        ;;
    esac
    sanitize_bounded_file "$file"
    size=$(wc -c < "$file")
    count=$((count + 1))
    total=$((total + size))
    if (( count > 32 || total > 2097152 )); then
      echo "diagnostics exceed the aggregate artifact bound" >&2
      return 1
    fi
  done < <(find "$root" -type f -print0)
  finalized="$root/finalized.txt"
  printf 'bounded=true redacted=true\n' > "$finalized"
  size=$(wc -c < "$finalized")
  count=$((count + 1))
  total=$((total + size))
  if (( count > 32 || total > 2097152 )); then
    rm -f "$finalized"
    echo "diagnostics exceed the aggregate artifact bound" >&2
    return 1
  fi
}

run_attempt() {
  local device=$1 attempt=$2 diagnostics_root=$3
  local destination="$diagnostics_root/$attempt"
  local log="$destination/flutter.log"
  local started_at=$SECONDS build_done_at=-1 body_started=false
  local classification=pre_body_failure status=0

  require_positive_integer IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS "$launch_timeout"
  require_positive_integer IOS_SMOKE_TOTAL_TIMEOUT_SECONDS "$total_timeout"
  require_positive_integer IOS_SMOKE_POLL_INTERVAL_SECONDS "$poll_interval"
  require_positive_integer IOS_SMOKE_DIAGNOSTIC_TIMEOUT_SECONDS "$diagnostic_timeout"
  [[ "$attempt" =~ ^[a-z0-9_-]+$ ]] || {
    echo "invalid attempt label: $attempt" >&2
    return 64
  }
  mkdir -p "$destination"
  : > "$log"

  (
    set -o pipefail
    "$flutter_bin" test --no-pub integration_test/mobile_smoke_test.dart \
      -d "$device" --reporter=expanded --timeout=3m \
      --dart-define=LANTERN_ENDPOINT=http://127.0.0.1:6380 \
      --dart-define=LANTERN_ALLOW_INSECURE=true 2>&1 | tee -a "$log"
  ) &
  local runner_pid=$!

  while kill -0 "$runner_pid" 2>/dev/null; do
    if [[ "$body_started" == false ]] && \
      grep -Fq 'MOBILE_SMOKE_BODY_STARTED' "$log"; then
      body_started=true
    fi
    if (( build_done_at < 0 )) && grep -Fq 'Xcode build done.' "$log"; then
      build_done_at=$SECONDS
    fi
    if (( build_done_at >= 0 )) && [[ "$body_started" == false ]] && \
      (( SECONDS - build_done_at >= launch_timeout )); then
      if grep -Fq 'MOBILE_SMOKE_BODY_STARTED' "$log"; then
        body_started=true
        continue
      fi
      mkdir -p "$destination/diagnostics"
      capture_bounded "$destination/diagnostics/process-tree-before-stop.txt" \
        "$ps_bin" -axo pid,ppid,state,etime,command || true
      if grep -Fq 'MOBILE_SMOKE_BODY_STARTED' "$log"; then
        body_started=true
        continue
      fi
      if ! kill -0 "$runner_pid" 2>/dev/null; then
        break
      fi
      terminate_process_tree "$runner_pid"
      wait "$runner_pid" || true
      capture_diagnostics "$device" "$destination/diagnostics"
      sanitize_bounded_file "$log"
      record_classification launch_stall "$destination"
      return 70
    fi
    if (( SECONDS - started_at >= total_timeout )); then
      if grep -Fq 'MOBILE_SMOKE_BODY_STARTED' "$log"; then
        body_started=true
      fi
      if [[ "$body_started" == true ]]; then
        classification=test_body_stall
      elif (( build_done_at >= 0 )); then
        classification=launch_stall
      elif grep -Fq 'Running Xcode build...' "$log"; then
        classification=build_stall
      else
        classification=pre_body_stall
      fi
      mkdir -p "$destination/diagnostics"
      capture_bounded "$destination/diagnostics/process-tree-before-stop.txt" \
        "$ps_bin" -axo pid,ppid,state,etime,command || true
      if grep -Fq 'MOBILE_SMOKE_BODY_STARTED' "$log"; then
        body_started=true
        classification=test_body_stall
      fi
      if ! kill -0 "$runner_pid" 2>/dev/null; then
        break
      fi
      terminate_process_tree "$runner_pid"
      wait "$runner_pid" || true
      capture_diagnostics "$device" "$destination/diagnostics"
      sanitize_bounded_file "$log"
      record_classification "$classification" "$destination"
      return 71
    fi
    sleep "$poll_interval"
  done

  wait "$runner_pid" || status=$?
  if grep -Fq 'MOBILE_SMOKE_BODY_STARTED' "$log"; then
    body_started=true
  fi
  if (( status == 0 )) && grep -Fq 'MOBILE_SMOKE_PASS ' "$log" && \
    grep -Fq 'All tests passed!' "$log"; then
    sanitize_bounded_file "$log"
    record_classification success "$destination"
    return 0
  fi
  if [[ "$body_started" == true ]]; then
    classification=test_failure
  elif grep -Fq 'Xcode build done.' "$log"; then
    classification=launch_failure
  elif grep -Fq 'Running Xcode build...' "$log"; then
    classification=build_failure
  fi
  capture_diagnostics "$device" "$destination/diagnostics"
  sanitize_bounded_file "$log"
  record_classification "$classification" "$destination"
  return 72
}

create_retry_device() {
  local source_device=$1 output_file=$2 diagnostics_root=$3
  local record runtime device_type retry_device name boot_status=0 boot_log
  mkdir -p "$diagnostics_root"
  record=$(
    "$xcrun_bin" simctl list devices available -j | jq -er \
      --arg id "$source_device" \
      '.devices | to_entries[] | .key as $runtime | .value[] |
       select(.udid == $id) | [$runtime, .deviceTypeIdentifier] | @tsv'
  )
  runtime=${record%%$'\t'*}
  device_type=${record#*$'\t'}
  [[ -n "$runtime" && -n "$device_type" && "$runtime" != "$device_type" ]]
  name="Lantern CI retry ${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}"
  retry_device=$(
    "$xcrun_bin" simctl create "$name" "$device_type" "$runtime"
  )
  [[ "$retry_device" =~ ^[0-9A-Fa-f-]{8,}$ ]]
  [[ "$retry_device" != "$source_device" ]]
  printf '%s\n' "$retry_device" > "$output_file"
  boot_log=$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/lantern-ios-retry-boot.XXXXXX")
  "$xcrun_bin" simctl boot "$retry_device" \
    > "$boot_log" 2>&1 || boot_status=$?
  if (( boot_status == 0 )); then
    "$xcrun_bin" simctl bootstatus "$retry_device" -b \
      >> "$boot_log" 2>&1 || boot_status=$?
  fi
  sanitize_bounded_file "$boot_log"
  mv "$boot_log" "$diagnostics_root/retry-device-boot.log"
  (( boot_status == 0 )) || return "$boot_status"
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    printf 'device_id=%s\n' "$retry_device" >> "$GITHUB_OUTPUT"
  fi
}

usage() {
  echo "usage: $0 run-attempt <device> <label> <diagnostics-root>" >&2
  echo "       $0 create-retry-device <source-device> <output-file> <diagnostics-root>" >&2
  echo "       $0 finalize-diagnostics <diagnostics-root>" >&2
  exit 64
}

case "${1:-}" in
  run-attempt)
    [[ $# -eq 4 ]] || usage
    run_attempt "$2" "$3" "$4"
    ;;
  create-retry-device)
    [[ $# -eq 4 ]] || usage
    create_retry_device "$2" "$3" "$4"
    ;;
  finalize-diagnostics)
    [[ $# -eq 2 ]] || usage
    finalize_diagnostics "$2"
    ;;
  *)
    usage
    ;;
esac
