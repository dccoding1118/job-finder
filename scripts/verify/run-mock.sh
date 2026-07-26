#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

"${SCRIPT_DIR}/reset.sh"
"${SCRIPT_DIR}/harness/deploy.sh"

binary="${VERIFY_BINARY}"
config="${MOCK_CONFIG}"
profile="${VERIFY_PROFILE}"
denylist="${VERIFY_DENYLIST}"
manifest="${ARTIFACT_MANIFEST}"
report="${EVIDENCE_ROOT}/$(timestamp)-mock.md"
output_file="$(mktemp "${RUNTIME_ROOT}/tmp/mock-output.XXXXXX")"
source_pid=""
api_unit=""
timer_unit=""
current_step=""
current_tag=""
current_title=""
total_steps=25
pass_count=0
# covered_r accumulates the requirement IDs actually exercised, so the answer
# sheet's coverage tally is derived from the steps that really ran.
declare -A covered_r=()

cleanup() {
  if [[ -n "${source_pid}" ]]; then
    # The fixture server drains keep-alive connections on SIGTERM, which can
    # outlive this script; force it down so it never lingers on its port.
    kill "${source_pid}" 2>/dev/null || true
    for _ in $(seq 1 20); do
      kill -0 "${source_pid}" 2>/dev/null || break
      sleep 0.1
    done
    kill -9 "${source_pid}" 2>/dev/null || true
  fi
  stop_transient_units
  rm -f "${output_file}"
}
trap cleanup EXIT

# begin_step <id> <tag> <title>; <tag> ties the step to its V case and the
# requirement(s) it verifies, e.g. "V2·R5/R7", aligning this answer sheet with
# docs/verify.md §4.
begin_step() {
  current_step="$1"
  current_tag="$2"
  current_title="$3"
  record ''
  record "## [${current_step}] (${current_tag}) ${current_title}"
  # Record every R token from the tag as exercised.
  local token
  for token in ${current_tag//·/ }; do
    case "${token}" in
      R*) IFS='/' read -ra _rs <<<"${token}"; for r in "${_rs[@]}"; do covered_r["${r}"]=1; done ;;
    esac
  done
}

assert_node() {
  mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" "$@"
}

# agent_call_total sums every recorded Agent call so the list capture path can be
# proven to add none.
agent_call_total() {
  "${binary}" verify snapshot --db "${MOCK_DB}" >"${RUNTIME_ROOT}/tmp/agent-total.json" || fail 'agent-call snapshot failed'
  assert_node agent-total "${RUNTIME_ROOT}/tmp/agent-total.json"
}

{
  printf '# jobfinder mock E2E 驗證報告\n\n'
  printf '%s\n' "- 產生時間：$(TZ=Asia/Taipei date --iso-8601=seconds)"
  printf '%s\n' '- 模式：mock（本機 Yourator fixture + fake CLI Agent）'
  printf '%s\n' '- 範圍：V1、V2、V4、V5；同一物化 artifact、SQLite 與 evidence'
  printf '%s\n' '- 答案卷：逐案例（S01–S25）觀察值＋判定，案例 ID 對齊題目卷 docs/verify.md §4'
  printf '%s\n' '- 資料保護：不記錄 Profile、JD、信件、token 或 Agent 原始輸出'
} >"${report}"

require_commands curl sha256sum systemd-analyze systemd-run systemctl xvfb-run
if ! systemd-run --user --wait --pipe --quiet /usr/bin/true >"${output_file}" 2>&1; then
  environment_blocked 'user systemd bus is unavailable'
fi
if ! mise exec -- node --version >"${output_file}" 2>&1; then
  environment_blocked 'mise-managed Node is unavailable'
fi
if ! mise exec -- npm exec --offline -- playwright --version >"${output_file}" 2>&1; then
  environment_blocked 'project-locked Playwright or its dependencies are unavailable; run mise run e2e-install'
fi

export PATH="${HARNESS_ROOT}/bin:${PATH}"

begin_step 'S01' 'V1·R8' '物化隔離 artifact'
expected_checksum="$(awk -F= '$1 == "binary_sha256" { print $2 }' "${manifest}")"
actual_checksum="$(sha256sum "${binary}" | awk '{print $1}')"
[[ -n "${expected_checksum}" && "${expected_checksum}" == "${actual_checksum}" ]] || fail 'artifact checksum mismatch'
require_file "${ARTIFACT_ROOT}/extension/manifest.json" 'deployed extension manifest'
require_file "${RUNTIME_ROOT}/systemd-mock/jobfinder-api.service" 'rendered verification API unit'
require_file "${RUNTIME_ROOT}/systemd-mock/jobfinder-run.service" 'rendered verification run unit'
require_file "${RUNTIME_ROOT}/systemd-mock/jobfinder-run.timer" 'rendered verification timer unit'
grep -Fqx "  path: ${MOCK_DB}" "${config}" || fail 'config does not target mock SQLite state'
record "- artifact：revision=$(awk -F= '$1 == "revision" { print $2 }' "${manifest}")；binary_sha256=${actual_checksum}"
pass_step

begin_step 'S02' 'V1·R1' '驗證隔離檔案權限'
assert_profile_perms "${config}"
record '- 驗收 root=0700；Profile、denylist、config=0600。'
pass_step

begin_step 'S03' 'V1·R1' '驗證匿名 Profile 與 PII 防線'
"${binary}" profile lint --profile "${profile}" --denylist "${denylist}" >"${output_file}" 2>&1 || fail 'profile lint did not pass'
"${binary}" profile show --profile "${profile}" --denylist "${denylist}" >"${output_file}" 2>&1 || fail 'profile summary did not pass'
grep -Fqx 'years_of_experience: 8' "${output_file}" || fail 'profile years are invalid'
grep -Fqx 'education: master (computer science)' "${output_file}" || fail 'profile education is invalid'
grep -Fqx 'expert: [Java]' "${output_file}" || fail 'profile expert skills are invalid'
grep -Fqx 'proficient: [Go]' "${output_file}" || fail 'profile proficient skills are invalid'
grep -Fqx 'directions: [P1:cloud architecture P2:backend engineering P3:platform reliability]' "${output_file}" || fail 'profile directions are invalid'
"${binary}" verify snapshot --db "${MOCK_DB}" >"${RUNTIME_ROOT}/tmp/schema-snapshot.json" || fail 'SQLite schema snapshot failed'
assert_node schema "${RUNTIME_ROOT}/tmp/schema-snapshot.json" >"${output_file}" 2>&1 || fail 'SQLite schema contract is invalid'
record '- 匿名 Profile summary、schema version=3、WAL、foreign keys、必要資料表與 revision 欄位已由物化 binary 建立。'
pass_step

begin_step 'S04' 'V2·R2' '產生本機 Yourator mock 資料來源'
source_requests="${RUNTIME_ROOT}/tmp/source-requests.jsonl"
"${binary}" verify mock-source --addr 127.0.0.1:18787 --request-log "${source_requests}" >"${RUNTIME_ROOT}/tmp/mock-source.log" 2>&1 &
source_pid=$!
for _ in $(seq 1 100); do
  curl --fail --silent http://127.0.0.1:18787/healthz >/dev/null && break
  sleep 0.1
done
if ! curl --fail --silent http://127.0.0.1:18787/healthz >/dev/null; then
  if grep -Eqi 'operation not permitted|permission denied|address already in use' "${RUNTIME_ROOT}/tmp/mock-source.log"; then
    environment_blocked 'loopback fixture server cannot bind its verification port'
  fi
  fail 'mock Yourator fixture server did not start'
fi
# A healthz answered by a stray process on the port would let later steps read a
# foreign request journal; require our own fixture process to still be alive.
if ! kill -0 "${source_pid}" 2>/dev/null; then
  environment_blocked 'verification fixture port is already held by another process'
fi
record '- source=yourator；external_id=1000–1003；內容均為合成資料；safe request journal 已啟用；GET /healthz → 200。'
pass_step

begin_step 'S05' 'V2·R5/R7' '抓取後手動驅動 filter 與 score（letter 階段零取件）'
if ! "${binary}" run --config "${config}" >"${output_file}" 2>&1; then
  fail 'first mock fetch did not complete'
fi
grep -Eq '^fetched: 4$' "${output_file}" || fail 'mock crawler did not fetch four fixture jobs'
grep -Eq '^new: 4$' "${output_file}" || fail 'first fetch did not insert four new jobs'
fetch_summary="$(tr '\n' ';' <"${output_file}" | sed 's/;$//')"
"${binary}" run --config "${config}" --stage filter --limit 30 >"${output_file}" 2>&1 || fail 'hand-driven filter stage did not complete'
grep -Eq '^filtered: 1$' "${output_file}" || fail 'first mock run filter summary is invalid'
"${binary}" run --config "${config}" --stage score --limit 30 >"${output_file}" 2>&1 || fail 'hand-driven score stage did not complete'
grep -Eq '^scored: 3$' "${output_file}" || fail 'first mock run did not enforce the daily score budget'
record "- fetch：${fetch_summary}；filter=1、score=3；未經使用者要求，letter 階段不取件、不生成求職信。"
pass_step

snapshot="${RUNTIME_ROOT}/tmp/positive-snapshot.json"
"${binary}" verify snapshot --db "${MOCK_DB}" >"${snapshot}" || fail 'positive SQLite snapshot failed'

begin_step 'S06' 'V2·R2' '驗證 production adapter 搜尋與 detail requests'
assert_node source "${source_requests}" >"${output_file}" 2>&1 || fail 'source request journal does not match the production adapter contract'
record '- 三個方向各形成一次搜尋；所有結果進入共同池，四個唯一 detail path 各請求一次。'
pass_step

begin_step 'S07' 'V2·R2' '驗證來源欄位正規化'
assert_node snapshot "${snapshot}" base >"${output_file}" 2>&1 || fail 'normalized source fields do not match the fixture contract'
record '- 四筆 Job 的 canonical URL、HTML 純文字、薪資、地點、remote type、company fallback 與 content hash 精確相符。'
pass_step

begin_step 'S08' 'V2·R3' '驗證 Profile 條件篩選'
grep -Fq '"external_id": "1000"' "${snapshot}" || fail 'filtered fixture is absent'
grep -Fq '"exclude_title_keywords"' "${snapshot}" || fail 'filter hit was not persisted'
record '- external_id=1000 命中 exclude_title_keywords 並走 new → filtered_out；其餘三筆進入評分。'
pass_step

begin_step 'S09' 'V2·R4' '驗證評分五維與分流且 letter 階段零 Agent 呼叫'
assert_node snapshot "${snapshot}" base >"${output_file}" 2>&1 || fail 'score, routing, or letter-idle state does not match the fixture contract'
grep -Fq '"total": 90' "${snapshot}" || fail 'approved score is absent'
grep -Fq '"total": 80' "${snapshot}" || fail 'retry score is absent'
grep -Fq '"total": 60' "${snapshot}" || fail 'low score is absent'
record '- 五維 90/80/60 經 Go 加權為 90/80/60，兩筆 shortlisted、一筆 scored；letter null、Drafter／Reviewer 呼叫數為零。'
pass_step

begin_step 'S10' 'V2·R5' '要求生成後驗證信件與 Agent 稽核'
mapfile -t shortlisted_ids < <("${binary}" jobs --db "${MOCK_DB}" --process-state shortlisted | awk '{print $1}')
[[ "${#shortlisted_ids[@]}" -eq 2 ]] || fail 'expected exactly two shortlisted jobs to request letters for'
for jid in "${shortlisted_ids[@]}"; do
  [[ "${jid}" =~ ^[0-9]+$ ]] || fail 'shortlisted Job ID is invalid'
  "${binary}" letter request --config "${config}" --job "${jid}" >"${output_file}" 2>&1 || fail "letter request for job ${jid} was not accepted"
  grep -Eq "^requested: ${jid}$" "${output_file}" || fail "letter request for job ${jid} did not confirm"
done
"${binary}" run --config "${config}" --stage letter --limit 30 >"${output_file}" 2>&1 || fail 'hand-driven letter stage did not complete'
grep -Eq '^lettered: 2$' "${output_file}" || fail 'letter stage did not consume the two requested jobs'
"${binary}" verify snapshot --db "${MOCK_DB}" >"${snapshot}" || fail 'lettered SQLite snapshot failed'
assert_node snapshot "${snapshot}" lettered >"${output_file}" 2>&1 || fail 'requested letters, transitions, or Agent audit do not match the fixture contract'
fake_checksum="$(sha256sum "${PROJECT_ROOT}/scripts/verify/harness/fake-agent.sh" | awk '{print $1}')"
[[ "$(sha256sum "${HARNESS_ROOT}/bin/claude" | awk '{print $1}')" == "${fake_checksum}" ]] || fail 'claude verifier executable is not the checked-in fake'
[[ "$(sha256sum "${HARNESS_ROOT}/bin/codex" | awk '{print $1}')" == "${fake_checksum}" ]] || fail 'codex verifier executable is not the checked-in fake'
"${binary}" jobs --db "${MOCK_DB}" --process-state letter_ready >"${output_file}" 2>&1 || fail 'ready letter is unreadable'
ready_id="$(awk 'NR == 1 { print $1 }' "${output_file}")"
"${binary}" jobs --db "${MOCK_DB}" --process-state letter_failed >"${output_file}" 2>&1 || fail 'failed letter is unreadable'
failed_id="$(awk 'NR == 1 { print $1 }' "${output_file}")"
[[ "${ready_id}" =~ ^[0-9]+$ && "${failed_id}" =~ ^[0-9]+$ ]] || fail 'letter fixture Job IDs are invalid'
record "- 對兩筆 shortlisted 要求生成後：job_id=${ready_id} approved/rounds=1/apply=pending；job_id=${failed_id} failed/rounds=3；轉換經 shortlisted→letter_requested；calls scorer=3、drafter=4、reviewer=4；兩個 Runner 均為 checked-in fake executable。"
pass_step

begin_step 'S11' 'V2·R7' '重跑抓取與階段驗證冪等'
if ! "${binary}" run --config "${config}" >"${output_file}" 2>&1; then
  fail 'second mock fetch did not complete'
fi
grep -Eq '^fetched: 4$' "${output_file}" || fail 'idempotent rerun did not re-fetch the fixture detail pages'
grep -Eq '^new: 0$' "${output_file}" || fail 'idempotent crawler rerun inserted duplicate jobs'
second_summary="$(tr '\n' ';' <"${output_file}" | sed 's/;$//')"
"${binary}" run --config "${config}" --stage filter --limit 30 >"${output_file}" 2>&1 || fail 'rerun filter stage did not complete'
grep -Eq '^filtered: 0$' "${output_file}" || fail 'idempotent rerun repeated filtering'
"${binary}" run --config "${config}" --stage score --limit 30 >"${output_file}" 2>&1 || fail 'rerun score stage did not complete'
grep -Eq '^scored: 0$' "${output_file}" || fail 'idempotent rerun repeated scoring'
"${binary}" verify snapshot --db "${MOCK_DB}" >"${snapshot}" || fail 'repeat SQLite snapshot failed'
assert_node snapshot "${snapshot}" repeat >"${output_file}" 2>&1 || fail 'repeat snapshot changed terminal data or Agent call counts'
record "- 重跑 fetch：${second_summary}；filter=0、score=0；Job=4、Score=3、Letter=2、Agent calls=11 均未增加。"
pass_step

begin_step 'S12' 'V1/V2·R8' '驗證安全 SQLite snapshot 與 Run stats'
grep -Fq '"fetched": 4' "${snapshot}" || fail 'fetch Run count is absent'
grep -Fq '"queries": 3' "${snapshot}" || fail 'query Run count is absent'
grep -Fq '"letters_failed"' "${snapshot}" && fail 'run stats still carry the retired worker-stage counters'
record '- snapshot 只含契約欄位與內容 hash；runs 只記抓取事實 fetched/new/queries/errors，不含 filter／score／letter 統計。'
pass_step

service_path="${HARNESS_ROOT}/bin:/usr/local/bin:/usr/bin:/bin"
begin_step 'S13' 'V4·R7' '驗證 rendered systemd units'
grep -Fqx "ExecStart=${binary} serve --config ${config}" "${RUNTIME_ROOT}/systemd-mock/jobfinder-api.service" || fail 'API unit does not target deployed artifact'
grep -Fqx "ExecStart=${binary} run --config ${config}" "${RUNTIME_ROOT}/systemd-mock/jobfinder-run.service" || fail 'run unit does not target deployed artifact'
grep -Fqx 'OnCalendar=*-*-* 08:30:00 Asia/Taipei' "${RUNTIME_ROOT}/systemd-mock/jobfinder-run.timer" || fail 'timer schedule is invalid'
if ! systemd-analyze --user verify \
  "${RUNTIME_ROOT}/systemd-mock/jobfinder-api.service" \
  "${RUNTIME_ROOT}/systemd-mock/jobfinder-run.service" \
  "${RUNTIME_ROOT}/systemd-mock/jobfinder-run.timer" >"${output_file}" 2>&1; then
  fail 'rendered production systemd units failed verification'
fi
record '- production templates 已物化；API/run ExecStart、PATH、DB 路徑與 Asia/Taipei timer 通過 systemd-analyze。'
pass_step

unit_suffix="$(date +%s)-$$"
oneshot_unit="jobfinder-verify-run-${unit_suffix}"
begin_step 'S14' 'V4·R7' '以 transient one-shot service 執行 artifact'
if ! systemd-run --user --wait --pipe --collect --quiet \
  --unit="${oneshot_unit}" \
  --property=Type=oneshot \
  --property="WorkingDirectory=${RUNTIME_ROOT}" \
  --setenv="PATH=${service_path}" \
  "${binary}" run --config "${config}" --stage filter --limit 1 >"${output_file}" 2>&1; then
  fail 'transient one-shot service did not complete'
fi
grep -Eq '^filtered: [0-9]+$' "${output_file}" || fail 'transient one-shot output is incomplete'
record '- systemd-run --user --wait --pipe 在 rendered PATH 下成功執行物化 binary。'
pass_step

api_unit="jobfinder-verify-api-${unit_suffix}"
begin_step 'S15' 'V4·R6/R7' '啟動帶常駐 worker 的 transient API service'
if ! systemd-run --user --collect --quiet \
  --unit="${api_unit}" \
  --property=Type=simple \
  --property="WorkingDirectory=${RUNTIME_ROOT}" \
  --setenv="PATH=${service_path}" \
  "${binary}" serve --config "${config}" >"${output_file}" 2>&1; then
  fail 'transient API service could not be started'
fi
api_url='http://127.0.0.1:18786/api/v1'
for _ in $(seq 1 50); do
  curl --fail --silent -H 'Authorization: Bearer verify-only-token' "${api_url}/jobs" >/dev/null && break
  sleep 0.1
done
curl --fail --silent -H 'Authorization: Bearer verify-only-token' "${api_url}/jobs" >/dev/null || fail 'transient API service did not become ready'
[[ "$(systemctl --user is-active "${api_unit}.service")" == 'active' ]] || fail 'transient API service is not active'
record '- user manager 以同一 binary、config 與 SQLite 啟動 loopback API service，內含常駐 worker。'
pass_step

timer_unit="jobfinder-verify-timer-${unit_suffix}"
begin_step 'S16' 'V4·R7' '以 transient timer 觸發 one-shot artifact'
if ! systemd-run --user --quiet \
  --unit="${timer_unit}" \
  --on-active=1s \
  --timer-property=AccuracySec=1us \
  --property=Type=oneshot \
  --property="WorkingDirectory=${RUNTIME_ROOT}" \
  --setenv="PATH=${service_path}" \
  "${binary}" run --config "${config}" --trigger timer >"${output_file}" 2>&1; then
  fail 'transient timer could not be scheduled'
fi
timer_result=''
timer_status=''
for _ in $(seq 1 100); do
  timer_result="$(systemctl --user show "${timer_unit}.service" -p Result --value 2>/dev/null || true)"
  timer_status="$(systemctl --user show "${timer_unit}.service" -p ExecMainStatus --value 2>/dev/null || true)"
  [[ "${timer_result}" == 'success' && "${timer_status}" == '0' ]] && break
  sleep 0.1
done
[[ "${timer_result}" == 'success' && "${timer_status}" == '0' ]] || fail 'transient timer did not trigger a successful one-shot service'
for _ in $(seq 1 50); do
  "${binary}" verify snapshot --db "${MOCK_DB}" >"${snapshot}" 2>/dev/null || true
  grep -Fq '"Trigger": "timer"' "${snapshot}" && break
  sleep 0.1
done
grep -Fq '"Trigger": "timer"' "${snapshot}" || fail 'timer-triggered fetch Run was not persisted'
record '- transient timer 以 trigger=timer 實際執行 fetch-only artifact；service Result=success、ExecMainStatus=0。'
pass_step

extension_origin='chrome-extension://oddnhajjhmgogefocnljofeahniodiei'
auth=(-H 'Authorization: Bearer verify-only-token' -H 'Content-Type: application/json')
begin_step 'S17' 'V4·R6' '驗證 localhost API 認證'
no_origin_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' -H 'Authorization: Bearer verify-only-token' "${api_url}/jobs")"
[[ "${no_origin_status}" == '200' ]] || fail "API MV3-style request returned HTTP ${no_origin_status}"
origin_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' -H "Origin: ${extension_origin}" -H 'Authorization: Bearer verify-only-token' "${api_url}/jobs")"
[[ "${origin_status}" == '200' ]] || fail "API exact origin request returned HTTP ${origin_status}"
preflight_status="$(curl --silent --output "${output_file}" --write-out '%{http_code}' -X OPTIONS -H "Origin: ${extension_origin}" -H 'Access-Control-Request-Method: GET' -H 'Access-Control-Request-Headers: authorization,content-type' "${api_url}/jobs")"
[[ "${preflight_status}" == '204' ]] || fail "API preflight returned HTTP ${preflight_status}"
record '- 精確 extension Origin＋token=200；無 Origin 的 privileged MV3 request＋token=200；preflight=204。'
pass_step

begin_step 'S18' 'V2/V4·R6' '驗證 API Job 清單、判定與對照'
curl --fail --silent "${auth[@]}" "${api_url}/jobs" >"${output_file}" || fail 'API did not expose pipeline jobs'
assert_node api "${output_file}" || fail 'API Job ordering, verdict, or letter_state is invalid'
for filter_case in 'source=yourator:source' 'process_state=letter_ready:process' 'apply_state=pending:apply' 'verdict=recommended:verdict'; do
  query="${filter_case%%:*}"
  expectation="${filter_case##*:}"
  curl --fail --silent "${auth[@]}" "${api_url}/jobs?${query}" >"${output_file}" || fail "API filter ${query} failed"
  assert_node api-filter "${output_file}" "${expectation}" || fail "API filter ${query} returned an invalid collection"
done
curl --fail --silent "${auth[@]}" "${api_url}/queue" >"${output_file}" || fail 'API queue is unreadable'
assert_node api-filter "${output_file}" queue || fail 'discovery queue should be empty before any list capture'
curl --fail --silent "${auth[@]}" "${api_url}/jobs/${ready_id}" >"${output_file}" || fail 'API did not expose Job detail'
assert_node api-detail "${output_file}" ready || fail 'approved API detail is invalid'
curl --fail --silent "${auth[@]}" "${api_url}/jobs/${failed_id}" >"${output_file}" || fail 'API did not expose failed Job detail'
assert_node api-detail "${output_file}" failed || fail 'failed API detail is invalid'
record "- source/process/apply/verdict filters、空 queue、job_id=${ready_id}/${failed_id} 的五維 Score、verdict／letter_state、Letter 與 StatusEvent 均與同一 SQLite 精確一致。"
pass_step

begin_step 'S19' 'V4·R6' '載入固定 ID 的 extension 模擬環境'
if ! VERIFY_ROOT="${VERIFY_ROOT}" VERIFY_EXTENSION_DIR="${ARTIFACT_ROOT}/extension" VERIFY_BROWSER_PROFILE="${RUNTIME_ROOT}/browser-profile-mock" VERIFY_EVIDENCE_ROOT="${EVIDENCE_ROOT}" xvfb-run -a mise exec -- node "${PROJECT_ROOT}/scripts/verify/browser/extension-e2e.js" >"${output_file}" 2>&1; then
  sed -n '1,120p' "${output_file}" >&2
  fail 'real MV3 extension verification failed'
fi
browser_evidence="${EVIDENCE_ROOT}/extension-browser.json"
require_file "${browser_evidence}" 'extension request evidence'
grep -Fq '"extension_id": "oddnhajjhmgogefocnljofeahniodiei"' "${browser_evidence}" || fail 'fixed unpacked extension ID was not observed'
grep -Fq '"connected": true' "${browser_evidence}" || fail 'MV3 dashboard did not connect'
grep -Fq '"options_token_cleared": true' "${browser_evidence}" || fail 'Options retained the token in the input field'
grep -Fq '"origin": "absent"' "${browser_evidence}" || fail 'MV3 request Origin behavior was not observed'
grep -Fq '"authorization_present": true' "${browser_evidence}" || fail 'MV3 request did not carry authorization'
record '- 固定 unpacked ID 已載入；安全摘要記錄 actual request 的 Origin=absent、Authorization present=true。'
pass_step

begin_step 'S20' 'V4·R6' '驗證 dashboard 判定篩選、copy 與 apply'
grep -Fq '"filters_verified": true' "${browser_evidence}" || fail 'dashboard source/process/apply/verdict filters were not verified'
grep -Fq '"detail_verified": true' "${browser_evidence}" || fail 'dashboard detail did not show the exact score, verdict and source fields'
grep -Fq '"copied": true' "${browser_evidence}" || fail 'dashboard copy action did not complete'
grep -Fq '"clipboard_matches": true' "${browser_evidence}" || fail 'clipboard did not contain the approved synthetic letter'
grep -Fq '"apply_requested": true' "${browser_evidence}" || fail 'dashboard apply action did not complete'
grep -Fq '"apply_state_after_response": "applied"' "${browser_evidence}" || fail 'dashboard apply response did not expose the persisted state'
"${binary}" jobs --db "${MOCK_DB}" --apply-state applied >"${output_file}" 2>&1 || fail 'extension apply result is unreadable'
grep -Eq "^${ready_id}[[:space:]]" "${output_file}" || fail 'extension apply transition was not persisted'
record "- dashboard 依 verdict／source／process／apply 篩選並顯示五維對照；clipboard 等於核准合成信件；job_id=${ready_id} apply_state=pending → applied。"
pass_step

begin_step 'S21' 'V4·R6' '驗證 dashboard 求職信生成入口'
grep -Fq '"generate_requested": true' "${browser_evidence}" || fail 'dashboard generate-letter action did not complete'
grep -Fq '"generate_letter_state_after_response": "requested"' "${browser_evidence}" || fail 'generate-letter response did not accept the request as letter_requested'
# The status_events log is permanent, so the re-request is provable after the
# resident worker has consumed it back to letter_failed.
requested_transitions=0
for _ in $(seq 1 100); do
  curl --fail --silent "${auth[@]}" "${api_url}/jobs/${failed_id}" >"${output_file}" || fail 'failed Job detail is unreadable'
  requested_transitions="$(grep -oE '"to_state": ?"letter_requested"' "${output_file}" | wc -l)"
  [[ "${requested_transitions}" -ge 2 ]] && break
  sleep 0.1
done
[[ "${requested_transitions}" -ge 2 ]] || fail 'dashboard generate entry did not add a new letter_requested transition'
record "- dashboard 對 job_id=${failed_id}（letter_failed）按下再次產生後受理並轉入 letter_requested；狀態事件永久記錄第二次 letter_requested，常駐 worker 隨後取件。"
pass_step

begin_step 'S22' 'V4·R7' '驗證 dashboard 手動抓取、Run history 與 browser evidence'
grep -Fq '"manual_run_requested": true' "${browser_evidence}" || fail 'dashboard manual fetch action did not complete'
grep -Fq '"run_history_rendered": true' "${browser_evidence}" || fail 'dashboard Run history did not render fetch stats and verdict distribution'
for _ in $(seq 1 100); do
  if curl --fail --silent "${auth[@]}" "${api_url}/runs" >"${output_file}" && grep -Fq '"trigger":"manual-extension"' "${output_file}"; then
    break
  fi
  sleep 0.1
done
grep -Fq '"trigger":"manual-extension"' "${output_file}" || fail 'manual extension fetch was not recorded'
require_file "${EVIDENCE_ROOT}/extension-dashboard.png" 'extension browser screenshot'
record '- dashboard Run history 顯示 trigger=manual-extension 與 fetch stats／verdict 分布；API 與同一 SQLite 一致；已保存 request 摘要與 screenshot。'
pass_step

begin_step 'S23' 'V4·R6' '驗證 extension mock browser L1'
if ! (cd "${PROJECT_ROOT}" && mise exec -- npm exec --offline -- playwright test --config scripts/verify/browser/playwright.config.js) >"${output_file}" 2>&1; then
  sed -n '1,160p' "${output_file}" >&2
  fail 'extension mock browser verification failed'
fi
browser_summary="$(tr '\n' ';' <"${output_file}" | sed 's/;$//')"
record "- Playwright：${browser_summary}"
pass_step

list_payload='{"source":"104","items":[{"href":"https://www.104.com.tw/job/v5intern","title":"backend intern engineer","company_name":"Alpha Co","location":"Taipei","salary_text":"month 90000","remote":false},{"href":"https://www.104.com.tw/job/v5senior","title":"Senior backend engineer","company_name":"Beta Co","location":"Taipei","salary_text":"month 120000","remote":true}]}'
begin_step 'S24' 'V5·R2/R3/R9' '驗證 104 清單就地判定且列表路徑零 Agent 呼叫'
calls_before="$(agent_call_total)"
curl --fail --silent "${auth[@]}" -X POST "${api_url}/capture/list" -d "${list_payload}" >"${output_file}" || fail '104 list capture failed'
assert_node capture-list "${output_file}" new || fail '104 list capture did not mark items by their synchronous verdict'
calls_after="$(agent_call_total)"
[[ "${calls_before}" == "${calls_after}" ]] || fail "list capture called an Agent (${calls_before} → ${calls_after})"
record '- 新職缺就地標記：intern 命中 exclude_title_keywords → filtered_out/unfit；senior → discovered/pending_detail；列表路徑 Agent 呼叫數不變。'
pass_step

begin_step 'S25' 'V5·R9' '驗證 104 既有職缺直接回判定與內頁 capture 非同步'
curl --fail --silent "${auth[@]}" -X POST "${api_url}/capture/list" -d "${list_payload}" >"${output_file}" || fail '104 list re-capture failed'
assert_node capture-list "${output_file}" repeat || fail '104 re-capture did not return the existing verdict without re-creating'
curl --fail --silent "${auth[@]}" "${api_url}/queue" >"${output_file}" || fail 'API queue is unreadable after list capture'
grep -Fq '"Senior backend engineer"' "${output_file}" || fail 'discovered list job is absent from the sidebar queue'
curl --fail --silent "${auth[@]}" -X POST "${api_url}/capture/job" \
  -d '{"source":"104","url":"https://www.104.com.tw/job/v5senior","dom":{"title":"Senior backend engineer","company_name":"Beta Co","location":"Taipei","description":"Build Go backend and cloud platform services","salary_text":"month 120000~150000","remote":true}}' \
  >"${output_file}" || fail '104 job capture failed'
assert_node capture-job "${output_file}" queued || fail '104 job capture did not screen synchronously and defer scoring'
record '- 既有職缺 re-capture 直接回現行判定且不重建；discovered 職缺進入 sidebar 待看清單；內頁補全文後同步過篩並停留 queued 待 worker 評分。'
pass_step

covered_r_list="$(printf '%s\n' "${!covered_r[@]}" | sort -V | paste -sd' ' -)"
record ''
record '## 結果'
record '- 結果：PASS'
record "- 判定 tally：PASS ${pass_count}／FAIL 0／SKIP 0（共 ${total_steps} 步）"
record "- 需求覆蓋：${covered_r_list}（本趟 mock 實際驗到的需求；完整地圖見 docs/verify.md §2）"
record '- 案例覆蓋：V1 隔離成品與匿名 Profile、V2 合成來源 fetch/worker 階段與按需求職信、V4 transient systemd lifecycle 與隔離 Chromium extension 模擬、V5 104 清單就地判定與內頁非同步評估。'
record ''
record '### 使用者故事重建（本趟驗過的劇本）'
record '載入 4 筆 Yourator 職缺 → #1000「intern」命中排除關鍵字當場篩掉（unfit）→ #1003 得 60（not_recommended）、#1001 得 80、#1002 得 90（後兩者 shortlisted，未要求不生成信）→ 對 2 筆 shortlisted 要求生成 → #1002 首輪核准（round 1）、#1001 三輪退回（round 3）→ dashboard 複製 #1002 的信、標記 applied → 104 搜尋頁載入 2 筆：intern 篩掉、senior 進待看清單 → senior 內頁補全文、過篩、排進評分佇列（queued）。'
record ''
record '> 對答案：逐案例（S01–S25）將上方觀察值對 docs/verify.md §4 的字面標準答案；測資與完整標準答案見 §3，機器斷言見 scripts/verify/oracle/assert-positive.mjs。'
printf 'mock verification passed: %s\n' "${report}"
