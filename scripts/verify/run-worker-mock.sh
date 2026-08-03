#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

# V7's worker cases are about timing rather than about a finished result: the
# switch has to stop a batch mid-flight, a single-job request has to cut ahead of
# one, and the scoring queue has to be drained before anything new is screened.
# They therefore run against their own SQLite, their own Profile copy and their
# own API port, with jobs synthesized through the capture route: the answer sheet
# of the main mock run must not depend on how long a fake Agent took.

binary="${VERIFY_BINARY}"
worker_db="${RUNTIME_ROOT}/worker-mock.db"
worker_profile="${RUNTIME_ROOT}/profile-worker.yaml"
worker_config="${RUNTIME_ROOT}/config-worker-mock.yaml"
output_file="${RUNTIME_ROOT}/tmp/worker-output.json"
snapshot="${RUNTIME_ROOT}/tmp/worker-snapshot.json"
journal="${RUNTIME_ROOT}/tmp/worker-journal.log"
headers="${RUNTIME_ROOT}/tmp/worker-headers.txt"
profile_json="${RUNTIME_ROOT}/tmp/profile-worker.json"
changed_profile_json="${RUNTIME_ROOT}/tmp/profile-worker-changed.json"
agent_signal="${RUNTIME_ROOT}/tmp/worker-agent-started"
report="${EVIDENCE_ROOT}/$(timestamp)-worker-mock.md"
api_url='http://127.0.0.1:18790/api/v1'
auth=(-H 'Authorization: Bearer verify-only-token' -H 'Content-Type: application/json')
api_unit=''
current_step=''
current_title=''
pass_count=0
total_steps=3
# The scan interval the worker runs at; a step that proves "nothing moved" waits
# several of them before it looks.
scan_interval=0.2
idle_wait=2

cleanup() {
  stop_transient_units
  rm -f "${output_file}" "${headers}" "${agent_signal}"
}
trap cleanup EXIT

begin_step() {
  current_step="$1"
  current_title="$2"
  record ''
  record "## [${current_step}] (${3:-V7}) ${current_title}"
}

assert_node() {
  mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" "$@"
}

json_field() {
  mise exec -- node -e 'const fs=require("fs");const value=JSON.parse(fs.readFileSync(process.argv[1]))[process.argv[2]];process.stdout.write(String(value===undefined?"":value));' "$1" "$2"
}

# write_config renders the worker config with one scoring budget. The budget is
# the only knob these cases turn: exhausting it is how a job is parked at
# `queued` without an Agent call, which is the state every timing case starts
# from.
write_config() {
  sed \
    -e "s|${MOCK_DB}|${worker_db}|g" \
    -e "s|${VERIFY_PROFILE}|${worker_profile}|g" \
    -e 's|127.0.0.1:18786|127.0.0.1:18790|g' \
    -e 's|max_filter_per_day: 8|max_filter_per_day: 400|' \
    -e "s|max_score_per_day: 8|max_score_per_day: $1|" \
    "${MOCK_CONFIG}" >"${worker_config}"
  chmod 600 "${worker_config}"
}

start_api() {
  local suffix="$1"
  local delay="${2:-}"
  if [[ -n "${api_unit}" ]]; then
    systemctl --user stop "${api_unit}.service" >/dev/null 2>&1 || true
    systemctl --user reset-failed "${api_unit}.service" >/dev/null 2>&1 || true
    # The resident worker holds an exclusive lock for as long as it runs, so the
    # replacement may only start once the previous one has actually released it.
    for _ in $(seq 1 200); do
      [[ -e "${worker_db}.worker.lock" ]] || break
      sleep 0.1
    done
    if [[ -e "${worker_db}.worker.lock" ]]; then
      fail 'the previous worker never released its lock'
    fi
  fi
  api_unit="jobfinder-verify-worker-${suffix}"
  local command=(systemd-run --user --collect --quiet --unit="${api_unit}" --property=Type=simple --property="WorkingDirectory=${RUNTIME_ROOT}" --setenv="PATH=${HARNESS_ROOT}/bin:/usr/local/bin:/usr/bin:/bin")
  if [[ -n "${delay}" ]]; then
    command+=(--setenv="JOBFINDER_VERIFY_AGENT_DELAY=${delay}" --setenv="JOBFINDER_VERIFY_AGENT_SIGNAL=${agent_signal}")
  fi
  command+=("${binary}" serve --config "${worker_config}")
  "${command[@]}" >/dev/null
  for _ in $(seq 1 200); do
    curl --fail --silent "${auth[@]}" "${api_url}/settings" >/dev/null && return
    sleep 0.1
  done
  fail 'the worker verification API did not become ready'
}

# read_journal collects the running service's own log, which is where the worker
# reports why it stopped, what it held back, and how much it picked up.
read_journal() {
  journalctl --user -u "${api_unit}.service" --no-pager -o cat >"${journal}" 2>/dev/null || fail 'the service log is unreadable'
}

agent_calls() {
  "${binary}" verify snapshot --db "${worker_db}" >"${snapshot}" || fail 'worker snapshot failed'
  assert_node agent-total "${snapshot}" "${1:-base}"
}

# settle_agents blocks until the worker stops adding Agent calls. It requires
# several consecutive equal readings: a paced fake Agent holds one call for a
# second, and two equal samples inside that second would read as settled.
settle_agents() {
  local previous='' current='' stable=0
  for _ in $(seq 1 200); do
    current="$(agent_calls)"
    if [[ "${previous}" == "${current}" ]]; then
      stable=$(( stable + 1 ))
      [[ "${stable}" -ge 4 ]] && return
    else
      stable=0
    fi
    previous="${current}"
    sleep 0.4
  done
  fail 'the worker never settled'
}

# wait_for_log blocks until the service records one line, which is how a step
# that asserts on a timing decision knows the worker has made it.
wait_for_log() {
  for _ in $(seq 1 200); do
    read_journal
    grep -Fq "$1" "${journal}" && return
    sleep 0.2
  done
  fail "the service log never recorded: $1"
}

set_auto() {
  curl --fail --silent "${auth[@]}" -X PUT -d "{\"auto_processing\":$1}" "${api_url}/settings" >"${output_file}" || fail 'the automatic processing switch could not be written'
  [[ "$(json_field "${output_file}" auto_processing)" == "$1" ]] || fail "the switch did not report ${1}"
}

# capture_job stores one synthetic full-text 104 job and prints its id. Every
# fixture is the same passing job under a different id: these cases are about
# how many jobs the worker takes and when, not about what it decides.
capture_job() {
  curl --fail --silent "${auth[@]}" -X POST "${api_url}/capture/job" \
    -d "{\"source\":\"104\",\"url\":\"https://www.104.com.tw/job/$1\",\"dom\":{\"title\":\"Worker backend engineer\",\"company_name\":\"Worker Co\",\"location\":\"Taipei\",\"description\":\"Build Go backend and cloud platform services\",\"salary_text\":\"月薪120,000~150,000元\",\"remote\":true}}" \
    >"${output_file}" || fail "capture of $1 failed"
  json_field "${output_file}" id
}

job_field() {
  curl --fail --silent "${auth[@]}" "${api_url}/jobs/$1" >"${output_file}" || fail "job $1 is unreadable"
  json_field "${output_file}" "$2"
}

state_of() { job_field "$1" process_state; }

# wait_for_state blocks until one job reaches a state, which is how a step that
# drives the worker knows the work it asked for is done.
wait_for_state() {
  for _ in $(seq 1 400); do
    [[ "$(state_of "$1")" == "$2" ]] && return
    sleep 0.2
  done
  fail "job $1 never reached $2 (it is $(state_of "$1"))"
}

# wait_for_verdict blocks until one job reaches any state the pipeline stops at.
wait_for_verdict() {
  local state=''
  for _ in $(seq 1 400); do
    state="$(state_of "$1")"
    case "${state}" in
      scored | shortlisted | filtered_out) return ;;
    esac
    sleep 0.2
  done
  fail "job $1 never reached a verdict (it is ${state})"
}

require_file "${binary}" 'verification binary'
require_file "${VERIFY_PROFILE}" 'verification profile'
require_file "${VERIFY_PROFILE_JSON}" 'synthetic Profile JSON'
require_file "${MOCK_CONFIG}" 'mock config'
require_commands curl systemd-run systemctl journalctl

rm -f "${worker_db}" "${worker_db}"* "${agent_signal}"
install -m 0600 "${VERIFY_PROFILE}" "${worker_profile}"
install -m 0600 "${VERIFY_PROFILE_JSON}" "${profile_json}"

{
  printf '# jobfinder worker mock E2E 驗證報告\n\n'
  printf '%s\n' "- 產生時間：$(TZ=Asia/Taipei date --iso-8601=seconds)"
  printf '%s\n' '- 模式：mock（合成職缺 capture + fake CLI Agent；獨立 SQLite 與 Profile 副本）'
  printf '%s\n' '- 範圍：V7 S39、S39B、S39C——自動處理開關、單筆插隊、Profile 變更後的等待中職缺與消化順序'
  printf '%s\n' '- 資料保護：不記錄 Profile、JD、信件、token 或 Agent 原始輸出'
} >"${report}"

# The scoring budget starts at three so the first six jobs split into three
# assessed and three parked at `queued` without any step having to race the
# worker for them.
write_config 3
start_api "$(date +%s)-$$-switch"

begin_step 'S39' '自動處理開關與單筆插隊處理'
batch_ids=()
for index in 1 2 3 4 5 6; do
  batch_ids+=("$(capture_job "wsw${index}")")
done
settle_agents
queued_ids=()
for jid in "${batch_ids[@]}"; do
  if [[ "$(state_of "${jid}")" == 'queued' ]]; then
    queued_ids+=("${jid}")
  fi
done
[[ "${#queued_ids[@]}" -eq 3 ]] || fail "expected the spent scoring budget to park three jobs at queued, got ${#queued_ids[@]}"
set_auto false
waiting_id="$(capture_job wswwaiting)"
spare_id="$(capture_job wswspare)"
calls_before="$(agent_calls)"
sleep "${idle_wait}"
for jid in "${waiting_id}" "${spare_id}"; do
  [[ "$(state_of "${jid}")" == 'new' ]] || fail "a new job was consumed while automatic processing was off (job ${jid})"
done
for jid in "${queued_ids[@]}"; do
  [[ "$(state_of "${jid}")" == 'queued' ]] || fail "a queued job was consumed while automatic processing was off (job ${jid})"
done
[[ "$(agent_calls)" == "${calls_before}" ]] || fail 'a switched-off worker still called an Agent'
# The day's scoring budget is spent, so this is also the case for a single-job
# request outrunning an exhausted budget.
budget_remaining="$(( 3 - $(agent_calls scorer) ))"
[[ "${budget_remaining}" -le 0 ]] || fail 'the scoring budget was not spent before the single-job request'
process_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X POST "${api_url}/jobs/${waiting_id}/process")"
[[ "${process_status}" == '202' ]] || fail "the single-job processing request returned HTTP ${process_status}"
wait_for_verdict "${waiting_id}"
settle_agents
calls_after="$(agent_calls)"
[[ "$(( calls_after - calls_before ))" -eq 2 ]] || fail "the single-job request cost ${calls_after} - ${calls_before} calls, want exactly one screening and one scoring"
[[ "$(state_of "${spare_id}")" == 'new' ]] || fail 'the single-job request consumed another job as well'
read_journal
grep -Fq 'automatic processing setting read' "${journal}" || fail 'the worker did not report reading the switch'

# The mid-batch case needs a call slow enough to switch the brake off during, and
# a budget that lets the batch start at all.
write_config 400
rm -f "${agent_signal}"
start_api "$(date +%s)-$$-pace" '1'
paced_before="$(agent_calls)"
set_auto true
for _ in $(seq 1 200); do
  [[ -f "${agent_signal}" ]] && break
  sleep 0.1
done
[[ -f "${agent_signal}" ]] || fail 'the delayed scorer never started, so nothing was in flight to switch off during'
set_auto false
wait_for_log 'stage stopped: automatic processing was switched off'
settle_agents
paced_after="$(agent_calls)"
[[ "$(( paced_after - paced_before ))" -le 1 ]] || fail "switching automatic processing off mid-batch finished $(( paced_after - paced_before )) further jobs, want at most the one in flight"
# Turning the switch back on resumes the jobs the batch left behind.
set_auto true
for jid in "${queued_ids[@]}" "${spare_id}"; do
  wait_for_verdict "${jid}"
done
settle_agents
record "- 關閉自動處理後，2 筆 new 與 3 筆 queued 於 ${idle_wait}s（scan_interval=${scan_interval}s）內狀態不變且 Agent 呼叫數維持 ${calls_before}；批次消化中關閉只再完成當下這一筆（$(( paced_after - paced_before )) 筆）並記一行 Info；每日評分額度用盡時 POST /jobs/${waiting_id}/process 仍回 202 並只增加該筆的 2 次呼叫；重新開啟後其餘職缺全部抵達最終判定。"
pass_step

begin_step 'S39B' 'Profile 變更後的等待中職缺'
# Jobs parked at `queued` under the current screening revision are what a hard
# rule change supersedes, so the budget is spent again to park two of them.
write_config "$(agent_calls scorer)"
start_api "$(date +%s)-$$-stale"
set_auto true
stale_ids=()
for index in 1 2; do
  stale_ids+=("$(capture_job "wsb${index}")")
done
for jid in "${stale_ids[@]}"; do
  wait_for_state "${jid}" queued
done
set_auto false
curl --silent --dump-header "${headers}" --output "${output_file}" "${auth[@]}" "${api_url}/profile" >/dev/null || fail 'the Profile is unreadable'
etag="$(awk 'tolower($1) == "etag:" { gsub("\r", "", $2); print $2 }' "${headers}" | tail -1)"
[[ -n "${etag}" ]] || fail 'the Profile did not return an ETag'
mise exec -- node -e 'const fs=require("fs");const [from,to]=process.argv.slice(1);const value=JSON.parse(fs.readFileSync(from));value.requirements.exclude_title_keywords.push("outsourcing");fs.writeFileSync(to,JSON.stringify(value));' "${profile_json}" "${changed_profile_json}"
save_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X PUT -H "If-Match: ${etag}" --data-binary "@${changed_profile_json}" "${api_url}/profile")"
[[ "${save_status}" == '200' ]] || fail "the hard-rule Profile change returned HTTP ${save_status}"
[[ "$(json_field "${output_file}" filter_changed)" == 'true' ]] || fail 'a changed hard rule did not change the screening revision'
new_revision="$(json_field "${output_file}" filter_revision)"
# The user did not press 更新過時判定職缺, so nothing is reprocessed in bulk.
write_config 400
start_api "$(date +%s)-$$-resume"
set_auto true
fresh_id="$(capture_job wsbfresh)"
wait_for_verdict "${fresh_id}"
[[ "$(job_field "${fresh_id}" filter_result_revision)" == "${new_revision}" ]] || fail 'a job screened after the change did not use the new revision'
settle_agents
for jid in "${stale_ids[@]}"; do
  [[ "$(state_of "${jid}")" == 'queued' ]] || fail "a job held back by a superseded screening left its state (job ${jid})"
done
wait_for_log 'jobs held back by a superseded screening revision'
grep -Eq 'jobs held back by a superseded screening revision.*jobs=2' "${journal}" || fail 'the worker did not report how many jobs a superseded screening holds back'
released_id="${stale_ids[0]}"
released_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X POST "${api_url}/jobs/${released_id}/process")"
[[ "${released_status}" == '202' ]] || fail "processing the held-back job returned HTTP ${released_status}"
wait_for_verdict "${released_id}"
[[ "$(job_field "${released_id}" filter_result_revision)" == "${new_revision}" ]] || fail 'the held-back job was not re-screened under the new revision'
record "- 儲存改動硬規則的 Profile 且不按「更新過時判定職缺」：開啟自動處理後新的 new 職缺 job_id=${fresh_id} 仍被消化並以新 filter_revision 完成篩選與評分；篩選已過時的 2 筆 queued 留在原狀態且 log 記一行待重新處理筆數；對 job_id=${released_id} 送 POST /jobs/{id}/process 後完成重篩與重評並切到新 revision。"
pass_step

begin_step 'S39C' '消化順序'
# More jobs than one scoring pass may take, so the drain has to come back for
# the rest before any screening starts.
write_config "$(agent_calls scorer)"
start_api "$(date +%s)-$$-order"
set_auto true
# The job S39B left held back by a superseded screening is still queued and is
# never picked up again, so the target is counted from where this step starts.
"${binary}" verify snapshot --db "${worker_db}" >"${snapshot}" || fail 'worker snapshot failed'
queued_target="$(( $(assert_node queued-count "${snapshot}") + 55 ))"
for index in $(seq 1 55); do
  capture_job "wsc${index}" >/dev/null
done
for _ in $(seq 1 400); do
  "${binary}" verify snapshot --db "${worker_db}" >"${snapshot}" || fail 'worker snapshot failed'
  [[ "$(assert_node queued-count "${snapshot}")" == "${queued_target}" ]] && break
  sleep 0.2
done
[[ "$(assert_node queued-count "${snapshot}")" == "${queued_target}" ]] || fail 'the 55 synthetic jobs were not all parked at queued'
set_auto false
settle_agents
last_id="$(capture_job wsclast)"
[[ "$(state_of "${last_id}")" == 'new' ]] || fail 'the trailing job did not stay at new'
# With no daily cap in the way, one pass is bounded by the batch size alone,
# which is the bound the repeated picking has to work around.
write_config 0
start_api "$(date +%s)-$$-drain"
set_auto true
wait_for_verdict "${last_id}"
settle_agents
read_journal
assert_node worker-order "${journal}" || fail 'the worker did not drain the scoring queue before screening anything new'
record "- 佇列同時有 55 筆 queued 與 1 筆 new：log 顯示 score stage 先以單次取件上限 50 取件、再取件消化剩餘 5 筆，之後 filter stage 才取 1 筆 new，且 job_id=${last_id} 於同一輪接著評分抵達最終判定。"
pass_step

record ''
record '## 結果'
record '- 結果：PASS'
record "- 判定 tally：PASS ${pass_count}／FAIL 0／SKIP 0（共 ${total_steps} 步）"
record '- 案例覆蓋：V7 S39、S39B、S39C；S37 與 S38 為實機 Chrome 人工 gate，不在本趟。'
printf 'worker mock verification passed: %s\n' "${report}"
