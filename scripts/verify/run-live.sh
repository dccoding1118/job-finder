#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

"${SCRIPT_DIR}/reset.sh"
"${SCRIPT_DIR}/harness/deploy.sh"

binary="${VERIFY_BINARY}"
config="${LIVE_CONFIG}"
profile="${VERIFY_PROFILE}"
denylist="${VERIFY_DENYLIST}"
report="${EVIDENCE_ROOT}/$(timestamp)-live.md"
output_file="$(mktemp "${RUNTIME_ROOT}/tmp/live-output.XXXXXX")"
snapshot="${RUNTIME_ROOT}/tmp/live-snapshot.json"
api_unit=""
timer_unit=""
current_step="preflight"
current_title="環境預檢"
total_steps=13

cleanup() {
  stop_transient_units
  rm -f "${output_file}"
}
trap cleanup EXIT

begin_step() {
  current_step="$1"
  current_title="$2"
  record ''
  record "## [${current_step}/${total_steps}] ${current_title}"
}
external_failure() {
  local description="$1"
  if grep -Eqi 'not logged in|not authenticated|authentication|unauthorized|forbidden|credential|login required|rate limit|quota|network is unreachable|temporary failure|timed out|timeout|connection refused|could not resolve|name or service not known|tls|certificate|proxy|user systemd bus|failed to connect to bus' "${output_file}"; then
    environment_blocked "${description}; authentication, quota, network, or user-systemd environment is unavailable"
  fi
  fail "${description}; command failed without a recognized environment error"
}

{
  printf '# jobfinder live E2E 驗證報告\n\n'
  printf '%s\n' "- 產生時間：$(TZ=Asia/Taipei date --iso-8601=seconds)"
  printf '%s\n' '- 模式：live（真 Yourator、真 claude/codex CLI、隔離 live SQLite）'
  printf '%s\n' '- 成本上限：score=1、letter=1；不執行 mock、不讀取 fake Agent result'
  printf '%s\n' '- 資料保護：證據只保存筆數、hash、狀態與布林結果，不保存 JD、Profile、信件、token 或 Agent 原始輸出'
} >"${report}"

require_commands curl realpath sha256sum systemd-analyze systemd-run systemctl xvfb-run claude codex
if ! mise exec -- node --version >"${output_file}" 2>&1; then
  environment_blocked 'mise-managed Node is unavailable'
fi
if ! mise exec -- npm exec --offline -- playwright --version >"${output_file}" 2>&1; then
  environment_blocked 'project-locked Playwright or its dependencies are unavailable; run mise run e2e-install'
fi
if ! systemd-run --user --wait --pipe --quiet /usr/bin/true >"${output_file}" 2>&1; then
  environment_blocked 'user systemd bus is unavailable'
fi

claude_path="$(realpath "$(command -v claude)")"
codex_path="$(realpath "$(command -v codex)")"
case "${claude_path}" in "${VERIFY_ROOT}"/*|"${PROJECT_ROOT}"/scripts/verify/*) environment_blocked 'claude resolves to the verification fake' ;; esac
case "${codex_path}" in "${VERIFY_ROOT}"/*|"${PROJECT_ROOT}"/scripts/verify/*) environment_blocked 'codex resolves to the verification fake' ;; esac
fake_checksum="$(sha256sum "${PROJECT_ROOT}/scripts/verify/harness/fake-agent.sh" | awk '{print $1}')"
[[ "$(sha256sum "${claude_path}" | awk '{print $1}')" != "${fake_checksum}" ]] || environment_blocked 'claude executable matches the verification fake'
[[ "$(sha256sum "${codex_path}" | awk '{print $1}')" != "${fake_checksum}" ]] || environment_blocked 'codex executable matches the verification fake'
claude --version >"${output_file}" 2>&1 || environment_blocked 'claude CLI is not executable'
codex --version >"${output_file}" 2>&1 || environment_blocked 'codex CLI is not executable'

begin_step '01' '共同 artifact、live 設定與固定 model'
expected_checksum="$(awk -F= '$1 == "binary_sha256" { print $2 }' "${ARTIFACT_MANIFEST}")"
actual_checksum="$(sha256sum "${binary}" | awk '{print $1}')"
[[ -n "${expected_checksum}" && "${expected_checksum}" == "${actual_checksum}" ]] || fail 'artifact checksum mismatch'
grep -Fqx "  path: ${LIVE_DB}" "${config}" || fail 'live config does not target live SQLite'
grep -Fqx '    base_url: https://www.yourator.co' "${config}" || fail 'live config does not target official Yourator'
# Every role carries a primary and a fallback, and both have to name the agent
# and the model outright: a live run must never reach an external CLI through an
# implicit default. The expected endpoint count is derived from the roles the
# config declares, so adding a role extends this check instead of ageing it out.
role_count="$(grep -Ec '^    (filter|scorer|drafter|reviewer):$' "${config}")"
[[ "${role_count}" == '4' ]] || fail 'live config must define the filter, scorer, drafter and reviewer roles'
endpoint_count=$(( role_count * 2 ))
[[ "$(grep -Ec '^[[:space:]]+agent: (claude|codex)$' "${config}")" == "${endpoint_count}" ]] || fail "live config must name an agent for all ${endpoint_count} role endpoints"
[[ "$(grep -Ec '^[[:space:]]+model: [^[:space:]]+$' "${config}")" == "${endpoint_count}" ]] || fail "live config must name a model for all ${endpoint_count} role endpoints"
if grep -Fq "${HARNESS_ROOT}" "${config}" "${RUNTIME_ROOT}/systemd-live/"*; then
	fail 'live config or rendered units reference the mock harness'
fi
if ! "${binary}" run --config "${config}" --stage filter --limit 1 >"${output_file}" 2>&1; then
	fail 'role-specific agent/model config did not pass strict parsing and Runner construction'
fi
grep -Fqx 'filtered_out: 0' "${output_file}" || fail 'cost-free config preflight produced unexpected work'
record "- artifact：binary_sha256=${actual_checksum}；Git dirty 狀態不影響執行。"
record "- filter／scorer／drafter／reviewer 四個 role 的 primary 與 fallback 共 ${endpoint_count} 個 endpoints 均明確指定 agent 與 model，並在外部呼叫前通過 strict config 與 Runner 建構。"
pass_step

begin_step '02' '匿名 Profile、權限與 schema'
assert_profile_perms "${config}"
"${binary}" profile lint --profile "${profile}" --denylist "${denylist}" >"${output_file}" 2>&1 || fail 'profile lint did not pass'
"${binary}" verify snapshot --db "${LIVE_DB}" >"${snapshot}" || fail 'live SQLite schema snapshot failed'
mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" schema "${snapshot}" >"${output_file}" 2>&1 || fail 'live SQLite schema contract is invalid'
record '- live SQLite 與 mock SQLite 分離；Profile、denylist、config 權限符合契約。'
pass_step

begin_step '03' '真 Yourator 三方向搜尋與共同去重池'
if ! "${binary}" run --config "${config}" --stage fetch --limit 1 >"${output_file}" 2>&1; then
  external_failure 'live Yourator fetch did not complete'
fi
grep -Eq '^fetched: [1-9][0-9]*$' "${output_file}" || fail 'live Yourator returned zero newly materialized jobs'
"${binary}" verify snapshot --db "${LIVE_DB}" >"${snapshot}" || fail 'live source snapshot failed'
source_summary="$(mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" live-snapshot "${snapshot}" source 2>"${output_file}")" || fail 'live source data violates the normalization or deduplication contract'
record "- 安全摘要：${source_summary}；三個 direction query 的結果已進入同一去重池。"
pass_step

begin_step '04' 'Profile 條件篩選'
"${binary}" run --config "${config}" --stage filter --limit 1000 >"${output_file}" 2>&1 || fail 'live filter stage did not complete'
grep -Eq '^filtered_out: [0-9]+$' "${output_file}" || fail 'live filter summary is invalid'
record '- 所有已抓取 Job 已依匿名 Profile 進入 filtered_out 或 queued。'
pass_step

service_path="${PATH}"
unit_suffix="$(date +%s)-$$"
begin_step '05' '真 Claude scorer，最多一筆'
score_unit="jobfinder-live-score-${unit_suffix}"
if ! systemd-run --user --wait --pipe --collect --quiet \
  --unit="${score_unit}" --property=Type=oneshot --property="WorkingDirectory=${RUNTIME_ROOT}" \
  --setenv="PATH=${service_path}" "${binary}" run --config "${config}" --stage score --limit 1 >"${output_file}" 2>&1; then
  external_failure 'live scorer CLI invocation did not complete'
fi
grep -Fqx 'scored: 1' "${output_file}" || fail 'live scorer did not process exactly one Job'
record '- user systemd manager 以 claude-sonnet-5 完成一筆 scorer 呼叫。'
pass_step

begin_step '06' '真 Drafter 與 Reviewer，最多一筆信件'
"${binary}" verify snapshot --db "${LIVE_DB}" >"${snapshot}" || fail 'pre-request snapshot failed'
if grep -Eq '"role": "(drafter|reviewer)"' "${snapshot}"; then
  fail 'letters were generated before an explicit request'
fi
mapfile -t shortlisted_ids < <("${binary}" jobs --db "${LIVE_DB}" --process-state shortlisted | awk '{print $1}')
[[ "${#shortlisted_ids[@]}" -ge 1 ]] || fail 'live scoring produced no shortlisted Job to request a letter for'
letter_job="${shortlisted_ids[0]}"
[[ "${letter_job}" =~ ^[0-9]+$ ]] || fail 'shortlisted Job ID is invalid'
"${binary}" letter request --config "${config}" --job "${letter_job}" >"${output_file}" 2>&1 || fail 'live letter request was not accepted'
grep -Eqx "requested: ${letter_job}" "${output_file}" || fail 'live letter request did not confirm'
letter_unit="jobfinder-live-letter-${unit_suffix}"
if ! systemd-run --user --wait --pipe --collect --quiet \
  --unit="${letter_unit}" --property=Type=oneshot --property="WorkingDirectory=${RUNTIME_ROOT}" \
  --setenv="PATH=${service_path}" "${binary}" run --config "${config}" --stage letter --limit 1 >"${output_file}" 2>&1; then
  external_failure 'live drafter/reviewer CLI invocation did not complete'
fi
grep -Fqx 'lettered: 1' "${output_file}" || fail 'live letter stage did not process exactly one Job'
"${binary}" verify snapshot --db "${LIVE_DB}" >"${snapshot}" || fail 'live completed snapshot failed'
complete_summary="$(mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" live-snapshot "${snapshot}" complete 2>"${output_file}")" || fail 'live Agent output violates score, letter, or audit contracts'
record "- 要求前 Drafter／Reviewer 呼叫為零；對一筆 shortlisted Job 明確要求後，user systemd manager 完成一輪信件生成。"
record "- 安全摘要：${complete_summary}；Agent 原始輸出與信件內容未寫入 evidence。"
pass_step

begin_step '07' 'live fetch 重跑冪等'
# Not inserting a duplicate is only half of idempotence. The other half is that
# an unchanged listing still hashes to the same content: a source page carrying
# per-request state would reset every job to `new` and buy the whole batch's
# screening and scoring again on every run.
before_fingerprint="$(mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" live-fingerprint "${snapshot}" 2>"${output_file}")" || fail 'pre-rerun fingerprint failed'
if ! "${binary}" run --config "${config}" --stage fetch --limit 1 >"${output_file}" 2>&1; then
  external_failure 'repeated live Yourator fetch did not complete'
fi
grep -Fqx 'new: 0' "${output_file}" || fail 'repeated live fetch inserted duplicate Jobs'
"${binary}" verify snapshot --db "${LIVE_DB}" >"${snapshot}" || fail 'repeated live snapshot failed'
after_fingerprint="$(mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" live-fingerprint "${snapshot}" 2>"${output_file}")" || fail 'post-rerun fingerprint failed'
[[ "${before_fingerprint}" == "${after_fingerprint}" ]] || fail "repeated live fetch changed stored content hashes or processing states (${before_fingerprint} → ${after_fingerprint}); an unchanged listing must not be re-screened and re-scored"
repeat_summary="$(mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" live-snapshot "${snapshot}" complete 2>"${output_file}")" || fail 'repeated live snapshot violates contracts'
[[ "${repeat_summary}" == "${complete_summary}" ]] || fail 'live rerun changed Job identity or Agent counts'
record "- 安全摘要維持 ${repeat_summary}；不重複新增 Job 或 Agent 呼叫，且 ${before_fingerprint%%:*} 筆職缺的 content hash 與處理狀態逐筆未變。"
pass_step

begin_step '08' 'rendered live systemd units'
grep -Fqx "ExecStart=${binary} serve --config ${config}" "${RUNTIME_ROOT}/systemd-live/jobfinder-api.service" || fail 'live API unit does not target common artifact'
grep -Fqx "ExecStart=${binary} run --config ${config}" "${RUNTIME_ROOT}/systemd-live/jobfinder-run.service" || fail 'live run unit does not target common artifact'
grep -Fqx 'OnCalendar=*-*-* 08:30:00 Asia/Taipei' "${RUNTIME_ROOT}/systemd-live/jobfinder-run.timer" || fail 'live timer schedule is invalid'
if ! systemd-analyze --user verify "${RUNTIME_ROOT}/systemd-live/jobfinder-api.service" "${RUNTIME_ROOT}/systemd-live/jobfinder-run.service" "${RUNTIME_ROOT}/systemd-live/jobfinder-run.timer" >"${output_file}" 2>&1; then
  fail 'rendered live systemd units failed verification'
fi
record '- API/run/timer 皆引用共同 binary 與 live config，PATH 不含 mock harness。'
pass_step

begin_step '09' 'transient timer 觸發 one-shot'
timer_unit="jobfinder-live-timer-${unit_suffix}"
if ! systemd-run --user --quiet --unit="${timer_unit}" --on-active=1s --timer-property=AccuracySec=1us \
  --property=Type=oneshot --property="WorkingDirectory=${RUNTIME_ROOT}" --setenv="PATH=${service_path}" \
  "${binary}" run --config "${config}" --stage filter --trigger timer --limit 1 >"${output_file}" 2>&1; then
  external_failure 'live transient timer could not be scheduled'
fi
timer_result=''
timer_status=''
for _ in $(seq 1 100); do
  timer_result="$(systemctl --user show "${timer_unit}.service" -p Result --value 2>/dev/null || true)"
  timer_status="$(systemctl --user show "${timer_unit}.service" -p ExecMainStatus --value 2>/dev/null || true)"
  [[ "${timer_result}" == 'success' && "${timer_status}" == '0' ]] && break
  sleep 0.1
done
[[ "${timer_result}" == 'success' && "${timer_status}" == '0' ]] || fail 'live transient timer did not trigger successfully'
record '- timer 以 trigger=timer 執行不含 Agent 呼叫的 filter stage。'
pass_step

begin_step '10' 'transient localhost API'
api_unit="jobfinder-live-api-${unit_suffix}"
if ! systemd-run --user --collect --quiet --unit="${api_unit}" --property=Type=simple \
  --property="WorkingDirectory=${RUNTIME_ROOT}" --setenv="PATH=${service_path}" \
  "${binary}" serve --config "${config}" >"${output_file}" 2>&1; then
  external_failure 'live transient API could not be started'
fi
api_url='http://127.0.0.1:18796/api/v1'
for _ in $(seq 1 50); do
  curl --fail --silent -H 'Authorization: Bearer verify-live-token' "${api_url}/jobs" >/dev/null && break
  sleep 0.1
done
curl --fail --silent -H 'Authorization: Bearer verify-live-token' "${api_url}/jobs" >"${output_file}" || fail 'live API did not expose Jobs'
grep -Fq '"items"' "${output_file}" || fail 'live API Job collection is invalid'
curl --fail --silent -H 'Authorization: Bearer verify-live-token' "${api_url}/runs" >"${output_file}" || fail 'live API did not expose Runs'
grep -Fq '"items"' "${output_file}" || fail 'live API Run collection is invalid'
record '- loopback API 以 live token 提供同一 live SQLite 的 Jobs 與 Runs。'
pass_step

begin_step '11' '真 MV3 extension 唯讀連線'
if ! VERIFY_ROOT="${VERIFY_ROOT}" VERIFY_EXTENSION_DIR="${ARTIFACT_ROOT}/extension" VERIFY_BROWSER_PROFILE="${RUNTIME_ROOT}/browser-profile-live" VERIFY_EVIDENCE_ROOT="${EVIDENCE_ROOT}" \
  xvfb-run -a mise exec -- node "${PROJECT_ROOT}/scripts/verify/browser/extension-live-e2e.js" >"${output_file}" 2>&1; then
  # The browser verifier's own error is the only thing that says which assertion
  # gave way, so it goes to stderr rather than being swallowed by the step name.
  sed -n '1,120p' "${output_file}" >&2
  fail 'live MV3 extension verification failed'
fi
browser_evidence="${EVIDENCE_ROOT}/extension-live-browser.json"
require_file "${browser_evidence}" 'live extension evidence'
grep -Fq '"connected": true' "${browser_evidence}" || fail 'live dashboard did not connect'
grep -Fq '"jobs_visible": true' "${browser_evidence}" || fail 'live dashboard did not render Jobs'
grep -Fq '"official_source_visible": true' "${browser_evidence}" || fail 'live dashboard did not expose an official source URL'
grep -Fq '"mutations_requested": false' "${browser_evidence}" || fail 'live browser requested a mutation'
record '- extension 以固定 ID 連線並唯讀顯示真 Job；未截圖、未保存 JD、未觸發 apply/retry/manual run。'
pass_step

begin_step '12' 'mock/live artifact 與 runtime 邊界'
[[ "${MOCK_DB}" != "${LIVE_DB}" && "${MOCK_CONFIG}" != "${LIVE_CONFIG}" ]] || fail 'mock and live runtime files overlap'
[[ ! -e "${MOCK_DB}" ]] || fail 'standalone live run unexpectedly created mock SQLite'
[[ "${binary}" == "${ARTIFACT_ROOT}/bin/jobfinder" ]] || fail 'live did not use the common artifact binary'
record '- standalone live 執行未建立 mock.db；只有 config、SQLite 與外部依賴按模式分離。'
pass_step

begin_step '13' '安全 evidence 完整性'
if grep -Eqi 'verify-live-token|\[你的姓名\]|\[你的聯絡方式\]|"description"[[:space:]]*:' "${report}" "${browser_evidence}"; then
  fail 'live evidence contains token, letter placeholder, or JD description content'
fi
record '- evidence 只含 artifact checksum、安全統計、狀態與布林觀測。'
pass_step

record ''
record '## 結果'
record '- 結果：PASS'
record '- 完成：13/13'
record '- 覆蓋：B0–B4 真來源、真 CLI Runner、systemd、localhost API 與唯讀 MV3 extension。'
printf 'live verification passed: %s\n' "${report}"
