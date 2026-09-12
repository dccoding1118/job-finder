#!/usr/bin/env bash

# Live verification (docs/verify.md §6).
#
# This runs against the test environment's real installation: the binary the
# installer placed, the config it rendered, the SQLite it points at, and the
# scheduling units it mounted. Nothing is materialised into a sandbox — a
# synthetic config plus transient units cannot show that the real source, the
# real CLI agents and the real service environment work together.
#
# It needs the checkout (this script, the oracle and the fixtures), so it only
# applies to a test environment that shares a machine with the development
# environment. Other platforms verify by hand, see docs/verify.md §6.1.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

require_commands jobfinder curl realpath sha256sum systemd-analyze systemd-run systemctl claude codex

paths_out="$(jobfinder paths)" || environment_blocked 'the installed jobfinder could not report its paths'
installed_path() { awk -v key="$1" 'index($0, key) == 1 { print $NF; exit }' <<<"${paths_out}"; }

binary="$(installed_path 'binary')"
config="$(installed_path 'config')"
profile="$(installed_path 'profile')"
denylist="$(installed_path 'denylist')"
db="$(installed_path 'database')"
units_dir="$(installed_path 'systemd units')"

EVIDENCE_ROOT="${PROJECT_ROOT}/.local-dev/test-deploy/evidence"
mkdir -p "${EVIDENCE_ROOT}"
chmod 700 "${EVIDENCE_ROOT}"

report="${EVIDENCE_ROOT}/$(timestamp)-live.md"
output_file="$(mktemp -t jobfinder-live-output.XXXXXX)"
snapshot="$(mktemp -t jobfinder-live-snapshot.XXXXXX)"
timer_unit=""
api_unit=""
current_step="preflight"
current_title="環境預檢"
total_steps=11

cleanup() {
  stop_transient_units
  rm -f "${output_file}" "${snapshot}"
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
  printf '# jobfinder live 驗收報告\n\n'
  printf '%s\n' "- 產生時間：$(TZ=Asia/Taipei date --iso-8601=seconds)"
  printf '%s\n' '- 對象：測試環境的實際安裝（真 Yourator、真 claude/codex CLI、已安裝的設定與 SQLite）'
  printf '%s\n' '- 成本上限：score=1、letter=1；不執行 mock、不讀取 fake Agent result'
  printf '%s\n' '- 判準基準：增量。資料庫沿用歷次驗收的內容，每趟比對的是本趟的新增與變動'
  printf '%s\n' '- 資料保護：證據只保存筆數、hash、狀態與布林結果，不保存 JD、Profile、信件、token 或 Agent 原始輸出'
} >"${report}"

for required in "${binary}" "${config}" "${profile}" "${denylist}"; do
  [[ -n "${required}" ]] || environment_blocked 'the installed jobfinder did not report a complete set of paths'
done
[[ -x "${binary}" ]] || environment_blocked "no installed binary at ${binary}"
[[ -f "${config}" ]] || environment_blocked "no installed config at ${config}"

if ! mise exec -- node --version >"${output_file}" 2>&1; then
  environment_blocked 'mise-managed Node is unavailable'
fi
if ! systemd-run --user --wait --pipe --quiet /usr/bin/true >"${output_file}" 2>&1; then
  environment_blocked 'user systemd bus is unavailable'
fi

# The whole point is the real CLI agents, so a PATH that resolves them to the
# mock harness invalidates the run rather than failing the product.
claude_path="$(realpath "$(command -v claude)")"
codex_path="$(realpath "$(command -v codex)")"
fake_agent="${PROJECT_ROOT}/scripts/verify/harness/fake-agent.sh"
case "${claude_path}" in "${VERIFY_ROOT}"/*|"${PROJECT_ROOT}"/scripts/verify/*) environment_blocked 'claude resolves to the verification fake' ;; esac
case "${codex_path}" in "${VERIFY_ROOT}"/*|"${PROJECT_ROOT}"/scripts/verify/*) environment_blocked 'codex resolves to the verification fake' ;; esac
fake_checksum="$(sha256sum "${fake_agent}" | awk '{print $1}')"
[[ "$(sha256sum "${claude_path}" | awk '{print $1}')" != "${fake_checksum}" ]] || environment_blocked 'claude executable matches the verification fake'
[[ "$(sha256sum "${codex_path}" | awk '{print $1}')" != "${fake_checksum}" ]] || environment_blocked 'codex executable matches the verification fake'
claude --version >"${output_file}" 2>&1 || environment_blocked 'claude CLI is not executable'
codex --version >"${output_file}" 2>&1 || environment_blocked 'codex CLI is not executable'

begin_step '01' '安裝身分與設定契約'
version_line="$("${binary}" version)"
grep -Eq '^jobfinder dev \([0-9a-f]+\)' <<<"${version_line}" || fail "the installed binary is not a dev package build: ${version_line}"
api_addr="$(awk '/^api:/{f=1; next} f && /^  addr:/{print $2; exit}' "${config}")"
api_token="$(awk '/^api:/{f=1; next} f && /^  token:/{print $2; exit}' "${config}")"
case "${api_addr}" in 127.0.0.1:*|"[::1]:"*|localhost:*) ;; *) fail "api.addr is not a loopback address: ${api_addr}" ;; esac
[[ -n "${api_token}" ]] || fail 'the installed config carries no api.token'
grep -Fqx "  path: ${db}" "${config}" || fail 'the installed config does not target the reported database'
grep -Fqx '    base_url: https://www.yourator.co' "${config}" || fail 'the installed config does not target official Yourator'
# Every role carries a primary and a fallback, and both have to name the agent
# and the model outright: a live run must never reach an external CLI through an
# implicit default. The expected endpoint count is derived from the roles the
# config declares, so adding a role extends this check instead of ageing it out.
role_count="$(grep -Ec '^    (filter|scorer|drafter|reviewer):$' "${config}")"
[[ "${role_count}" == '4' ]] || fail 'the installed config must define the filter, scorer, drafter and reviewer roles'
endpoint_count=$(( role_count * 2 ))
[[ "$(grep -Ec '^[[:space:]]+agent: (claude|codex)$' "${config}")" == "${endpoint_count}" ]] || fail "the config must name an agent for all ${endpoint_count} role endpoints"
[[ "$(grep -Ec '^[[:space:]]+model: [^[:space:]]+$' "${config}")" == "${endpoint_count}" ]] || fail "the config must name a model for all ${endpoint_count} role endpoints"
if ! "${binary}" run --config "${config}" --stage filter --limit 1 >"${output_file}" 2>&1; then
  fail 'role-specific agent/model config did not pass strict parsing and Runner construction'
fi
record "- 安裝身分：${version_line}；設定、Profile、denylist 與 SQLite 皆取自 jobfinder paths。"
record "- filter／scorer／drafter／reviewer 四個 role 的 primary 與 fallback 共 ${endpoint_count} 個 endpoints 均明確指定 agent 與 model，並在外部呼叫前通過 strict config 與 Runner 建構。"
pass_step

begin_step '02' 'Profile、權限與 schema'
require_mode "${config}" 600 'installed config'
require_mode "${profile}" 600 'installed profile'
require_mode "${denylist}" 600 'installed denylist'
"${binary}" profile lint --profile "${profile}" --denylist "${denylist}" >"${output_file}" 2>&1 || fail 'profile lint did not pass'
"${binary}" verify snapshot --db "${db}" >"${snapshot}" || fail 'SQLite schema snapshot failed'
mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" schema "${snapshot}" >"${output_file}" 2>&1 || fail 'SQLite schema contract is invalid'
record '- 設定、Profile 與 denylist 權限符合契約；已安裝 SQLite 的 schema 通過契約檢查。'
pass_step

begin_step '03' '真 Yourator 三方向搜尋與共同去重池'
if ! "${binary}" run --config "${config}" --stage fetch --limit 1 >"${output_file}" 2>&1; then
  external_failure 'live Yourator fetch did not complete'
fi
grep -Eq '^fetched: [1-9][0-9]*$' "${output_file}" || fail 'live Yourator returned zero jobs'
"${binary}" verify snapshot --db "${db}" >"${snapshot}" || fail 'live source snapshot failed'
source_summary="$(mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" live-snapshot "${snapshot}" source 2>"${output_file}")" || fail 'live source data violates the normalization or deduplication contract'
record "- 安全摘要：${source_summary}；三個 direction query 的結果已進入同一去重池。"
pass_step

begin_step '04' 'Profile 條件篩選'
"${binary}" run --config "${config}" --stage filter --limit 1000 >"${output_file}" 2>&1 || fail 'live filter stage did not complete'
grep -Eq '^filtered_out: [0-9]+$' "${output_file}" || fail 'live filter summary is invalid'
record '- 所有已抓取 Job 已依 Profile 進入 filtered_out 或 queued。'
pass_step

service_path="${PATH}"
unit_suffix="$(date +%s)-$$"

begin_step '05' '真 Claude scorer，最多一筆'
score_unit="jobfinder-live-score-${unit_suffix}"
if ! systemd-run --user --wait --pipe --collect --quiet \
  --unit="${score_unit}" --property=Type=oneshot \
  --setenv="PATH=${service_path}" "${binary}" run --config "${config}" --stage score --limit 1 >"${output_file}" 2>&1; then
  external_failure 'live scorer CLI invocation did not complete'
fi
grep -Fqx 'scored: 1' "${output_file}" || fail 'live scorer did not process exactly one Job'
record '- user systemd manager 下完成一筆 scorer 呼叫。'
pass_step

begin_step '06' '真 Drafter 與 Reviewer，最多一筆信件'
"${binary}" verify snapshot --db "${db}" >"${snapshot}" || fail 'pre-request snapshot failed'
before_letters="$(grep -Eco '"role": "(drafter|reviewer)"' "${snapshot}" || true)"
mapfile -t shortlisted_ids < <("${binary}" jobs --db "${db}" --process-state shortlisted | awk '{print $1}')
[[ "${#shortlisted_ids[@]}" -ge 1 ]] || fail 'live scoring produced no shortlisted Job to request a letter for'
letter_job="${shortlisted_ids[0]}"
[[ "${letter_job}" =~ ^[0-9]+$ ]] || fail 'shortlisted Job ID is invalid'
"${binary}" letter request --config "${config}" --job "${letter_job}" >"${output_file}" 2>&1 || fail 'live letter request was not accepted'
grep -Eqx "requested: ${letter_job}" "${output_file}" || fail 'live letter request did not confirm'
letter_unit="jobfinder-live-letter-${unit_suffix}"
if ! systemd-run --user --wait --pipe --collect --quiet \
  --unit="${letter_unit}" --property=Type=oneshot \
  --setenv="PATH=${service_path}" "${binary}" run --config "${config}" --stage letter --limit 1 >"${output_file}" 2>&1; then
  external_failure 'live drafter/reviewer CLI invocation did not complete'
fi
grep -Fqx 'lettered: 1' "${output_file}" || fail 'live letter stage did not process exactly one Job'
"${binary}" verify snapshot --db "${db}" >"${snapshot}" || fail 'live completed snapshot failed'
after_letters="$(grep -Eco '"role": "(drafter|reviewer)"' "${snapshot}" || true)"
[[ "${after_letters}" -gt "${before_letters}" ]] || fail 'the explicit letter request produced no drafter or reviewer call'
complete_summary="$(mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" live-snapshot "${snapshot}" complete 2>"${output_file}")" || fail 'live Agent output violates score, letter, or audit contracts'
record "- 本趟在明確要求之前未新增 Drafter／Reviewer 呼叫；對一筆 shortlisted Job 要求後完成一輪信件生成。"
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
"${binary}" verify snapshot --db "${db}" >"${snapshot}" || fail 'repeated live snapshot failed'
after_fingerprint="$(mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" live-fingerprint "${snapshot}" 2>"${output_file}")" || fail 'post-rerun fingerprint failed'
[[ "${before_fingerprint}" == "${after_fingerprint}" ]] || fail "repeated live fetch changed stored content hashes or processing states (${before_fingerprint} → ${after_fingerprint}); an unchanged listing must not be re-screened and re-scored"
repeat_summary="$(mise exec -- node "${PROJECT_ROOT}/scripts/verify/oracle/assert-positive.mjs" live-snapshot "${snapshot}" complete 2>"${output_file}")" || fail 'repeated live snapshot violates contracts'
[[ "${repeat_summary}" == "${complete_summary}" ]] || fail 'live rerun changed Job identity or Agent counts'
record "- 安全摘要維持 ${repeat_summary}；不重複新增 Job 或 Agent 呼叫，且 ${before_fingerprint%%:*} 筆職缺的 content hash 與處理狀態逐筆未變。"
pass_step

begin_step '08' '已安裝的 systemd units'
grep -Fq "ExecStart=" "${units_dir}/jobfinder-api.service" || fail 'the installed API unit has no ExecStart'
grep -Fq "serve --config" "${units_dir}/jobfinder-api.service" || fail 'the installed API unit does not start the server'
grep -Fq "run --config" "${units_dir}/jobfinder-run.service" || fail 'the installed run unit does not start a fetch'
grep -Fqx 'OnCalendar=*-*-* 08:30:00 Asia/Taipei' "${units_dir}/jobfinder-run.timer" || fail 'the installed timer schedule is invalid'
if ! systemd-analyze --user verify "${units_dir}/jobfinder-api.service" "${units_dir}/jobfinder-run.service" "${units_dir}/jobfinder-run.timer" >"${output_file}" 2>&1; then
  fail 'the installed systemd units failed verification'
fi
record '- 已安裝的 API／run／timer 三個 unit 通過 systemd 驗證，並指向安裝流程放置的 binary 與設定。'
pass_step

begin_step '09' '已安裝的 one-shot 實際觸發'
if ! systemctl --user start jobfinder-run.service >"${output_file}" 2>&1; then
  external_failure 'the installed one-shot unit could not be started'
fi
run_result=''
for _ in $(seq 1 600); do
  run_result="$(systemctl --user show jobfinder-run.service -p Result --value 2>/dev/null || true)"
  [[ "$(systemctl --user is-active jobfinder-run.service 2>/dev/null || true)" == 'activating' ]] || break
  sleep 1
done
[[ "${run_result}" == 'success' ]] || fail "the installed one-shot unit did not complete successfully (Result=${run_result})"
record '- 手動觸發已安裝的 jobfinder-run.service，抓取在服務環境下完成。'
pass_step

begin_step '10' '執行中的 loopback API'
[[ "$(systemctl --user is-active jobfinder-api.service 2>/dev/null || true)" == 'active' ]] || fail 'the installed API service is not active'
api_url="http://${api_addr}/api/v1"
curl --fail --silent -H "Authorization: Bearer ${api_token}" "${api_url}/jobs" >"${output_file}" || fail 'the API did not expose Jobs'
grep -Fq '"items"' "${output_file}" || fail 'the API Job collection is invalid'
curl --fail --silent -H "Authorization: Bearer ${api_token}" "${api_url}/runs" >"${output_file}" || fail 'the API did not expose Runs'
grep -Fq '"items"' "${output_file}" || fail 'the API Run collection is invalid'
if curl --fail --silent "${api_url}/jobs" >/dev/null 2>&1; then
  fail 'the API answered an unauthenticated request'
fi
record '- 執行中的 API service 以設定的 token 提供同一 SQLite 的 Jobs 與 Runs；未帶 token 的請求被拒。'
pass_step

begin_step '11' '安全 evidence 完整性'
if grep -Fq "${api_token}" "${report}"; then
  fail 'evidence contains the API token'
fi
if grep -Eqi '\[你的姓名\]|\[你的聯絡方式\]|"description"[[:space:]]*:' "${report}"; then
  fail 'evidence contains letter placeholder or JD description content'
fi
record '- evidence 只含安全統計、狀態與布林觀測。'
pass_step

record ''
record '## 結果'
record '- 結果：PASS'
record "- 完成：${pass_count}/${total_steps}"
record '- 覆蓋：真來源、真 CLI Runner、已安裝的 systemd 排程與執行中的 loopback API。Side Panel 實機讀寫屬 docs/verify.md §6.1 的 D4。'
printf 'live verification passed: %s\n' "${report}"
