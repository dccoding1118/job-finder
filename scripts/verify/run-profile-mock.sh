#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

binary="${VERIFY_BINARY}"
profile_db="${RUNTIME_ROOT}/profile-mock.db"
profile_path="${RUNTIME_ROOT}/profile-v7.yaml"
profile_config="${RUNTIME_ROOT}/config-profile-mock.yaml"
profile_json="${RUNTIME_ROOT}/tmp/profile-v7.json"
modified_json="${RUNTIME_ROOT}/tmp/profile-v7-modified.json"
race_json="${RUNTIME_ROOT}/tmp/profile-v7-race.json"
invalid_json="${RUNTIME_ROOT}/tmp/profile-v7-invalid.json"
headers="${RUNTIME_ROOT}/tmp/profile-headers.txt"
output_file="${RUNTIME_ROOT}/tmp/profile-output.json"
snapshot="${RUNTIME_ROOT}/tmp/profile-snapshot.json"
before_save_snapshot="${RUNTIME_ROOT}/tmp/profile-snapshot-before-save.json"
agent_signal="${RUNTIME_ROOT}/tmp/profile-agent-started"
report="${EVIDENCE_ROOT}/$(timestamp)-profile-mock.md"
api_url='http://127.0.0.1:18788/api/v1'
auth=(-H 'Authorization: Bearer verify-only-token' -H 'Content-Type: application/json')
api_unit=''
current_step=''
current_title=''
pass_count=0
total_steps=9

cleanup() {
  stop_transient_units
  rm -f "${output_file}" "${headers}" "${invalid_json}" "${race_json}" "${agent_signal}" "${before_save_snapshot}"
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

start_api() {
  local suffix="$1"
  local delay="${2:-}"
  if [[ -n "${api_unit}" ]]; then
    systemctl --user stop "${api_unit}.service" >/dev/null 2>&1 || true
    systemctl --user reset-failed "${api_unit}.service" >/dev/null 2>&1 || true
  fi
  api_unit="jobfinder-verify-profile-${suffix}"
  local command=(systemd-run --user --collect --quiet --unit="${api_unit}" --property=Type=simple --property="WorkingDirectory=${RUNTIME_ROOT}" --setenv="PATH=${HARNESS_ROOT}/bin:/usr/local/bin:/usr/bin:/bin")
  if [[ -n "${delay}" ]]; then
    command+=(--setenv="JOBFINDER_VERIFY_AGENT_DELAY=${delay}" --setenv="JOBFINDER_VERIFY_AGENT_SIGNAL=${agent_signal}")
  fi
  command+=("${binary}" serve --config "${profile_config}")
  "${command[@]}" >/dev/null
  for _ in $(seq 1 100); do
    curl --fail --silent "${auth[@]}" "${api_url}/profile" >/dev/null && return
    sleep 0.1
  done
  fail 'Profile verification API did not become ready'
}

# wait_for_quiet_agents blocks until the resident worker has stopped adding Agent
# calls, so a step that must prove "no new call" is not sampling mid-reprocess.
wait_for_quiet_agents() {
  local previous='' current=''
  for _ in $(seq 1 200); do
    "${binary}" verify snapshot --db "${profile_db}" >"${snapshot}" 2>/dev/null || true
    current="$(assert_node agent-total "${snapshot}")"
    [[ -n "${previous}" && "${previous}" == "${current}" ]] && return
    previous="${current}"
    sleep 0.3
  done
  fail 'the resident worker never settled'
}

etag_from_headers() {
  awk 'tolower($1) == "etag:" { gsub("\r", "", $2); print $2 }' "${headers}" | tail -1
}

require_file "${binary}" 'verification binary'
require_file "${VERIFY_PROFILE_JSON}" 'synthetic Profile JSON'
require_file "${MOCK_CONFIG}" 'mock config'
require_file "${MOCK_DB}" 'completed mock SQLite state'
require_commands curl sha256sum systemd-run systemctl

cp "${MOCK_DB}" "${profile_db}"
cp "${VERIFY_PROFILE_JSON}" "${profile_json}"
sed \
  -e "s|${MOCK_DB}|${profile_db}|g" \
  -e "s|${VERIFY_PROFILE}|${profile_path}|g" \
  -e 's|127.0.0.1:18786|127.0.0.1:18788|g' \
  "${MOCK_CONFIG}" >"${profile_config}"
chmod 600 "${profile_config}" "${profile_json}"
rm -f "${profile_path}" "${profile_db}.worker.lock" "${agent_signal}"

{
  printf '# jobfinder Profile mock E2E 驗證報告\n\n'
  printf '%s\n' "- 產生時間：$(TZ=Asia/Taipei date --iso-8601=seconds)"
  printf '%s\n' '- 模式：mock（合成 Profile；不保存 request／response body）'
  printf '%s\n' '- 範圍：V7 S30–S36、V8 S52 與 S46；S37 與 S38 為實機 Chrome 人工 gate'
} >"${report}"

start_api "$(date +%s)-$$-setup"

begin_step 'S30' '驗證 missing setup mode'
curl --silent --dump-header "${headers}" --output "${output_file}" "${auth[@]}" "${api_url}/profile"
grep -Fq '"status":"missing"' "${output_file}" || fail 'missing Profile status was not returned'
grep -Fiq 'ETag: "missing"' "${headers}" || fail 'missing Profile ETag was not returned'
jobs_status="$(curl --silent --output /dev/null --write-out '%{http_code}' "${auth[@]}" "${api_url}/jobs")"
runs_status="$(curl --silent --output /dev/null --write-out '%{http_code}' "${auth[@]}" "${api_url}/runs")"
run_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X POST "${api_url}/runs")"
[[ "${jobs_status}" == '200' && "${runs_status}" == '200' && "${run_status}" == '409' ]] || fail 'setup mode did not keep reads available and processing paused'
"${binary}" verify snapshot --db "${profile_db}" >"${snapshot}"
assert_node schema-migrated "${snapshot}" || fail 'setup database did not migrate to the current schema'
record '- Profile status=missing、ETag="missing"；Job 讀取=200、手動 run=409；worker 暫停且 schema_version=10。'
pass_step

begin_step 'S31' '建立 Profile 並立即啟用'
create_status="$(curl --silent --dump-header "${headers}" --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X PUT -H 'If-Match: "missing"' --data-binary "@${profile_json}" "${api_url}/profile")"
[[ "${create_status}" == '200' ]] || fail "first Profile save returned HTTP ${create_status}"
assert_node profile-response "${output_file}" create || fail 'first Profile save response is invalid'
etag="$(etag_from_headers)"
[[ -n "${etag}" ]] || fail 'first Profile save did not return ETag'
require_mode "${profile_path}" 600 'saved Profile'
record '- 合成 Profile 以 If-Match="missing" 建立；YAML=0600；provider 無重啟即 ready，回傳 revision 與 ETag，未自動重新處理既有職缺。'
pass_step

begin_step 'S32' '變更 Profile 後手動重處理既有 Job'
"${binary}" verify snapshot --db "${profile_db}" >"${before_save_snapshot}"
mise exec -- node -e 'const fs=require("fs");const [from,to]=process.argv.slice(1);const value=JSON.parse(fs.readFileSync(from));value.qualifications.certifications.push({name:"Cloud Architect",status:"renewing"});fs.writeFileSync(to,JSON.stringify(value));' "${profile_json}" "${modified_json}"
change_status="$(curl --silent --dump-header "${headers}" --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X PUT -H "If-Match: ${etag}" --data-binary "@${modified_json}" "${api_url}/profile")"
[[ "${change_status}" == '200' ]] || fail "Profile save returned HTTP ${change_status}"
assert_node profile-response "${output_file}" change || fail 'Profile save response is invalid'
etag="$(etag_from_headers)"
revision="$(mise exec -- node -e 'const fs=require("fs");process.stdout.write(JSON.parse(fs.readFileSync(process.argv[1])).filter_revision)' "${output_file}")"
"${binary}" verify snapshot --db "${profile_db}" >"${snapshot}"
cmp -s "${before_save_snapshot}" "${snapshot}" || fail 'Profile save changed existing Job revisions before explicit reprocess'
reprocess_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X POST "${api_url}/profile/reprocess")"
[[ "${reprocess_status}" == '200' ]] || fail "Profile reprocess returned HTTP ${reprocess_status}"
assert_node profile-reprocess-response "${output_file}" change || fail 'Profile reprocess statistics are invalid'
"${binary}" verify snapshot --db "${profile_db}" >"${snapshot}"
grep -Fq "${revision}" "${snapshot}" || fail 'eligible jobs did not switch to the new Profile revision'
record '- PUT 後舊 Job 保留原 revision；POST reprocess 後 partial Job 重新篩選、全文 Job 重新排隊，letter/apply 歷史保留。'
pass_step

begin_step 'S33' '驗證相同語意儲存冪等'
wait_for_quiet_agents
calls_before="$(assert_node agent-total "${snapshot}")"
same_status="$(curl --silent --dump-header "${headers}" --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X PUT -H "If-Match: ${etag}" --data-binary "@${modified_json}" "${api_url}/profile")"
[[ "${same_status}" == '200' ]] || fail "idempotent Profile save returned HTTP ${same_status}"
assert_node profile-response "${output_file}" same || fail 'same Profile was not a semantic no-op'
etag="$(etag_from_headers)"
wait_for_quiet_agents
calls_after="$(assert_node agent-total "${snapshot}")"
[[ "${calls_before}" == "${calls_after}" ]] || fail 'same Profile increased Agent calls'
record '- revision 不變、semantic_changed=false，未觸發重新處理且 Agent 呼叫數未增加。'
pass_step

begin_step 'S34' '驗證外部檔案修改衝突'
printf '\n# external formatting change\n' >>"${profile_path}"
external_hash="$(sha256sum "${profile_path}" | awk '{print $1}')"
conflict_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X PUT -H "If-Match: ${etag}" --data-binary "@${modified_json}" "${api_url}/profile")"
[[ "${conflict_status}" == '412' ]] || fail "stale ETag returned HTTP ${conflict_status}"
[[ "$(sha256sum "${profile_path}" | awk '{print $1}')" == "${external_hash}" ]] || fail 'conflicting save overwrote the external file'
grep -Fq 'profile_conflict' "${output_file}" || fail 'conflict response did not use the safe error code'
record '- 外部修改精確 bytes 後，舊 ETag 儲存=412；磁碟內容未被覆蓋。'
pass_step

sed -i 's/max_score_per_day: 8/max_score_per_day: 100/' "${profile_config}"
rm -f "${agent_signal}"
start_api "$(date +%s)-$$-race" '2s'
curl --silent --dump-header "${headers}" --output "${output_file}" "${auth[@]}" "${api_url}/profile"
etag="$(etag_from_headers)"

begin_step 'S35' '驗證 unknown field 與 PII 安全拒絕'
before_invalid_hash="$(sha256sum "${profile_path}" | awk '{print $1}')"
mise exec -- node -e 'const fs=require("fs");const [from,to,kind]=process.argv.slice(1);const value=JSON.parse(fs.readFileSync(from));if(kind==="unknown")value.unknown_field=true;else value.honesty_bounds.push("synthetic@example.invalid");fs.writeFileSync(to,JSON.stringify(value));' "${modified_json}" "${invalid_json}" unknown
unknown_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X PUT -H "If-Match: ${etag}" --data-binary "@${invalid_json}" "${api_url}/profile")"
[[ "${unknown_status}" == '422' ]] || fail "unknown field returned HTTP ${unknown_status}"
mise exec -- node -e 'const fs=require("fs");const [from,to]=process.argv.slice(1);const value=JSON.parse(fs.readFileSync(from));value.honesty_bounds.push("synthetic@example.invalid");fs.writeFileSync(to,JSON.stringify(value));' "${modified_json}" "${invalid_json}"
pii_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X PUT -H "If-Match: ${etag}" --data-binary "@${invalid_json}" "${api_url}/profile")"
[[ "${pii_status}" == '422' ]] || fail "synthetic PII returned HTTP ${pii_status}"
grep -Fq 'profile_invalid' "${output_file}" || fail 'invalid Profile response did not use the safe error code'
grep -Fq 'synthetic@example.invalid' "${output_file}" && fail 'PII response leaked the rejected value'
[[ "$(sha256sum "${profile_path}" | awk '{print $1}')" == "${before_invalid_hash}" ]] || fail 'invalid Profile changed the file'
record '- unknown field 與合成 PII 均回 422 safe issues；回應不含命中值，YAML 與 snapshot 未變。'
pass_step

begin_step 'S36' '驗證 in-flight Score 的 revision CAS'
# The worker only holds a score in flight if something is waiting to be scored,
# so requeue first and let the delayed fake scorer pick that job up.
mise exec -- node -e 'const fs=require("fs");const [from,to]=process.argv.slice(1);const value=JSON.parse(fs.readFileSync(from));value.intents.industry_interests.push("platform tooling");fs.writeFileSync(to,JSON.stringify(value));' "${modified_json}" "${race_json}"
requeue_status="$(curl --silent --dump-header "${headers}" --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X PUT -H "If-Match: ${etag}" --data-binary "@${race_json}" "${api_url}/profile")"
[[ "${requeue_status}" == '200' ]] || fail "score-gate Profile update returned HTTP ${requeue_status}"
etag="$(etag_from_headers)"
curl --fail --silent --output /dev/null "${auth[@]}" -X POST "${api_url}/profile/reprocess" || fail 'requeue for the in-flight race failed'
cp "${race_json}" "${modified_json}"
for _ in $(seq 1 100); do
  [[ -f "${agent_signal}" ]] && break
  sleep 0.1
done
[[ -f "${agent_signal}" ]] || fail 'delayed scorer did not start'
mise exec -- node -e 'const fs=require("fs");const [from,to]=process.argv.slice(1);const value=JSON.parse(fs.readFileSync(from));value.intents.content_likes.push("Race verification interest");fs.writeFileSync(to,JSON.stringify(value));' "${modified_json}" "${race_json}"
race_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X PUT -H "If-Match: ${etag}" --data-binary "@${race_json}" "${api_url}/profile")"
[[ "${race_status}" == '200' ]] || fail "race Profile update returned HTTP ${race_status}"
race_reprocess_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X POST "${api_url}/profile/reprocess")"
[[ "${race_reprocess_status}" == '200' ]] || fail "race Profile reprocess returned HTTP ${race_reprocess_status}"
for _ in $(seq 1 300); do
  "${binary}" verify snapshot --db "${profile_db}" >"${snapshot}" 2>/dev/null || true
  if assert_node profile-race "${snapshot}" >/dev/null 2>&1; then break; fi
  sleep 0.1
done
assert_node profile-race "${snapshot}" || fail 'old revision Score became current or audit revisions were lost'
record '- scorer 執行中切換 Profile：舊 Agent call 保留舊 revision；舊結果 CAS 未成為現行 Score；新 revision Score 可被查得。'
pass_step

begin_step 'S52' '只改軟規則的重跑範圍' 'V8'
curl --silent --dump-header "${headers}" --output "${output_file}" "${auth[@]}" "${api_url}/profile"
etag="$(etag_from_headers)"
mise exec -- node -e 'const fs=require("fs");const [from,to]=process.argv.slice(1);const value=JSON.parse(fs.readFileSync(from));value.intents.content_dislikes.push("on-call rotations without tooling");fs.writeFileSync(to,JSON.stringify(value));' "${race_json}" "${modified_json}"
wait_for_quiet_agents
cp "${snapshot}" "${before_save_snapshot}"
filter_calls_before="$(assert_node agent-total "${before_save_snapshot}" filter)"
intents_status="$(curl --silent --dump-header "${headers}" --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X PUT -H "If-Match: ${etag}" --data-binary "@${modified_json}" "${api_url}/profile")"
[[ "${intents_status}" == '200' ]] || fail "soft-rule-only Profile save returned HTTP ${intents_status}"
assert_node profile-response "${output_file}" intents || fail 'a soft-rule-only change did not leave the filter revision alone'
intents_reprocess_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' "${auth[@]}" -X POST "${api_url}/profile/reprocess")"
[[ "${intents_reprocess_status}" == '200' ]] || fail "soft-rule-only reprocess returned HTTP ${intents_reprocess_status}"
wait_for_quiet_agents
assert_node screened-intact "${snapshot}" || fail 'a soft-rule-only reprocess disturbed a structurally rejected job'
filter_calls_after="$(assert_node agent-total "${snapshot}" filter)"
[[ "${filter_calls_before}" == "${filter_calls_after}" ]] || fail "soft-rule-only reprocess re-screened jobs (filter calls ${filter_calls_before} → ${filter_calls_after})"
record "- 只修改 intents：PUT 回 filter_changed=false／score_changed=true；reprocess 後 filtered_out 職缺的狀態與逐條判定不變，role=filter 呼叫數維持 ${filter_calls_after}。"
pass_step

begin_step 'S46' 'Agent 用量稽核與每日彙總' 'V1/V2'
curl --silent --output "${output_file}" "${auth[@]}" "${api_url}/status" || fail 'status read failed'
usage_line="$(mise exec -- node -e '
const fs = require("fs");
const status = JSON.parse(fs.readFileSync(process.argv[1]));
const calls = status.agent_calls || [];
if (!calls.length) throw new Error("no audited agent calls");
const priced = calls.filter((call) => call.runner === "claude");
if (!priced.length) throw new Error("no claude calls to account for");
for (const call of priced) {
  if (call.model !== "claude-sonnet-5") throw new Error(`call ${call.id} recorded model ${call.model}`);
  if (!(call.input_tokens > 0 && call.output_tokens > 0 && call.cost_usd > 0)) throw new Error(`call ${call.id} recorded no usage`);
}
const usage = status.agent_usage_daily || [];
const rows = usage.filter((row) => row.runner === "claude" && row.model === "claude-sonnet-5");
if (!rows.length) throw new Error("no daily usage row for the runner that answered");
const sum = (field) => rows.reduce((total, row) => total + row[field], 0);
const [billed, input, output] = [sum("calls"), sum("input_tokens"), sum("output_tokens")];
if (!(billed > 0 && input === billed * 1200 && output === billed * 150 && sum("cache_read_tokens") === billed * 800 && sum("cache_write_tokens") === billed * 40)) {
  throw new Error(`daily totals do not match the per-call usage: ${JSON.stringify(rows)}`);
}
if (!(sum("cost_usd") > 0)) throw new Error("daily cost was not accumulated");
process.stdout.write(`${rows.map((row) => row.date).join(",")} ${billed} ${input} ${output}`);
' "${output_file}")" || fail 'agent usage accounting did not match the recorded calls'
read -r usage_date usage_calls usage_input usage_output <<<"${usage_line}"
record "- GET /status：每筆 Agent 呼叫附 model 與用量；agent_usage_daily 於 ${usage_date} 彙總 claude/claude-sonnet-5 共 ${usage_calls} 次呼叫、入 ${usage_input}／出 ${usage_output} token 且費用 > 0。"
pass_step

record ''
record '## 結果'
record '- 結果：PASS'
record "- 判定 tally：PASS ${pass_count}／FAIL 0／SKIP 0（共 ${total_steps} 步）"
record '- 案例覆蓋：V7 S30–S36、V8 S52 與 S46；S37 與 S38 為實機 Chrome 人工 gate，由驗收者操作，不在本趟。'
printf 'profile mock verification passed: %s\n' "${report}"
