#!/usr/bin/env bash

# Deployment verification, automated group (docs/verify.md §6.1).
#
# `internal/paths` is the single decision point for every location, and on this
# platform it derives all of them from $HOME and the XDG variables. Pointing HOME
# at an isolated root therefore yields an environment with no prior install while
# the installer still runs exactly the code path a real install runs.
#
# Two boundaries keep the real user environment untouched. The isolated root
# holds every file the install writes. A stub `systemctl` ahead of PATH absorbs
# the scheduler calls, so `enable` and `restart` can never reach the unit names a
# production install owns; what the installer asked the scheduler to do is
# recorded instead, and the rendered units are exercised as transient units, the
# same method docs/deploy.md §1 prescribes.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

DEPLOY_ROOT="${VERIFY_ROOT}/deploy"
FAKE_HOME="${DEPLOY_ROOT}/home"
STAGE_ROOT="${DEPLOY_ROOT}/stage"
SHIM_DIR="${DEPLOY_ROOT}/shim"
SYSTEMCTL_LOG="${DEPLOY_ROOT}/systemctl-calls.log"
DEPLOY_PORT="${DEPLOY_PORT:-18788}"
MOCK_SOURCE_PORT="${MOCK_SOURCE_PORT:-18789}"

INSTALLED_BIN="${FAKE_HOME}/.local/bin/jobfinder"
INSTALLED_CONFIG="${FAKE_HOME}/.config/jobfinder/config.yaml"
INSTALLED_UNITS="${FAKE_HOME}/.config/systemd/user"
INSTALLED_DB="${FAKE_HOME}/.local/share/jobfinder/jobs.db"

report=""
current_step=""
current_title=""
pass_count=0
api_unit=""
timer_unit=""
api_token=""
mock_source_pid=""

cleanup() {
  local status=$?
  stop_transient_units
  if [[ -n "${mock_source_pid:-}" ]]; then
    # The fixture server drains keep-alive connections on SIGTERM and can
    # outlive this script; force it down so it never lingers on its port.
    kill "${mock_source_pid}" 2>/dev/null || true
    for _ in $(seq 1 20); do
      kill -0 "${mock_source_pid}" 2>/dev/null || break
      sleep 0.1
    done
    kill -9 "${mock_source_pid}" 2>/dev/null || true
  fi
  if [[ -n "${api_unit:-}" ]]; then
    systemctl --user stop "${api_unit}.service" >/dev/null 2>&1 || true
  fi
  # The isolated root is the whole footprint of this run; nothing outside it was
  # written, so removing it returns the machine to its prior state.
  if [[ "${DEPLOY_KEEP_ROOT:-0}" != "1" ]]; then
    rm -rf "${DEPLOY_ROOT}"
  fi
  return "${status}"
}
trap cleanup EXIT

step() {
  current_step="$1"
  current_title="$2"
  record ''
  record "## ${current_step} ${current_title}"
}

# assert_contains fails the current step unless the file holds the substring.
assert_contains() {
  grep -qF -- "$2" "$1" || fail "$3"
}

assert_absent() {
  grep -qF -- "$2" "$1" && fail "$3"
  return 0
}

# staged_binary builds a jobfinder carrying the given version tag into its own
# directory. The installer refuses to install a resident copy over itself, so
# every install and update needs a build that lives outside the install root.
staged_binary() {
  local tag="$1"
  local dir="${STAGE_ROOT}/${tag}"
  mkdir -p "${dir}"
  (
    cd "${PROJECT_ROOT}"
    go build -ldflags "-X github.com/dccoding1118/job-finder/internal/version.tag=${tag}" \
      -o "${dir}/jobfinder" ./cmd/jobfinder
  )
  printf '%s\n' "${dir}/jobfinder"
}

# install_as runs an installer subcommand inside the isolated environment.
install_as() {
  local binary="$1"
  local action="$2"
  shift 2
  env HOME="${FAKE_HOME}" \
    XDG_CONFIG_HOME="${FAKE_HOME}/.config" \
    XDG_DATA_HOME="${FAKE_HOME}/.local/share" \
    PATH="${SHIM_DIR}:${PATH}" \
    "${binary}" "${action}" --assets "${PROJECT_ROOT}" --skip-verify "$@"
}

config_value() {
  sed -n "s/^  $1: //p" "${INSTALLED_CONFIG}" | head -1
}

api_status() {
  local path="$1"
  local token="${2:-}"
  if [[ -n "${token}" ]]; then
    curl -so /dev/null -w '%{http_code}' -H "Authorization: Bearer ${token}" \
      "http://127.0.0.1:${DEPLOY_PORT}${path}" || printf '000'
  else
    curl -so /dev/null -w '%{http_code}' "http://127.0.0.1:${DEPLOY_PORT}${path}" || printf '000'
  fi
}

wait_for_api() {
  for _ in $(seq 1 40); do
    if [[ "$(api_status /api/v1/jobs "${api_token}")" == "200" ]]; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

# ---------------------------------------------------------------- preflight

require_commands curl go sha256sum systemd-run systemctl
command -v mise >/dev/null || environment_blocked "missing required command: mise"

for port in "${DEPLOY_PORT}" "${MOCK_SOURCE_PORT}"; do
  if ss -lnt 2>/dev/null | grep -q ":${port} "; then
    environment_blocked "port ${port} is already in use; set DEPLOY_PORT / MOCK_SOURCE_PORT to free ports"
  fi
done

rm -rf "${DEPLOY_ROOT}"
mkdir -p "${FAKE_HOME}" "${STAGE_ROOT}" "${SHIM_DIR}" "${EVIDENCE_ROOT}"
chmod 700 "${VERIFY_ROOT}" "${DEPLOY_ROOT}" "${FAKE_HOME}"

cat > "${SHIM_DIR}/systemctl" <<SHIM
#!/usr/bin/env bash
# Absorbs the installer's scheduler calls so a verification run can never
# enable, restart or stop the units a production install owns.
printf 'systemctl %s\n' "\$*" >> "${SYSTEMCTL_LOG}"
case "\$*" in
  *"show-environment"*) exit 0 ;;
  *"-p ActiveState"*)   printf 'active\n' ;;
  *"-p LoadState"*)     printf 'loaded\n' ;;
  *"-p MainPID"*)       printf '0\n' ;;
  *)                    : ;;
esac
exit 0
SHIM

cat > "${SHIM_DIR}/loginctl" <<'SHIM'
#!/usr/bin/env bash
printf 'yes\n'
exit 0
SHIM

chmod 755 "${SHIM_DIR}/systemctl" "${SHIM_DIR}/loginctl"
: > "${SYSTEMCTL_LOG}"

report="${EVIDENCE_ROOT}/$(timestamp)-deploy.md"
{
  printf '# 部署驗收自動組 — %s\n' "$(TZ=Asia/Taipei date '+%Y-%m-%d %H:%M:%S %Z')"
  # The backticks below are Markdown, not command substitution.
  # shellcheck disable=SC2016
  printf '\n判準見 `docs/verify.md` §6.1。隔離根 `%s`，API 埠 %s。\n' "${DEPLOY_ROOT}" "${DEPLOY_PORT}"
} > "${report}"

printf '==> 建置兩個版號的 binary\n'
BIN_V1="$(staged_binary v0.0.1-deploy-a)"
BIN_V2="$(staged_binary v0.0.2-deploy-b)"

# ---------------------------------------------------------------- D1

step D1 "全新安裝"
record "- 動作：\`HOME=<隔離根> jobfinder install --assets <checkout>\`"

install_as "${BIN_V1}" install > "${DEPLOY_ROOT}/d1-install.log" 2>&1 \
  || fail "install 失敗，輸出見 ${DEPLOY_ROOT}/d1-install.log"

[[ -x "${INSTALLED_BIN}" ]] || fail "常駐 binary 未落在 ${INSTALLED_BIN}"
[[ -f "${INSTALLED_CONFIG}" ]] || fail "設定未落在 ${INSTALLED_CONFIG}"

# The rendered config must carry no placeholder and no path outside the root
# this install was pointed at.
assert_absent "${INSTALLED_CONFIG}" 'CHANGE_ME' "設定仍含 CHANGE_ME 佔位"
assert_absent "${INSTALLED_CONFIG}" '.local-dev/profile.yaml' "設定仍含未替換的範例路徑佔位"
for key in path denylist; do
  value="$(config_value "${key}")"
  [[ "${value}" == "${FAKE_HOME}"* ]] || fail "設定的 ${key} 指向安裝根之外：${value}"
done

require_mode "${INSTALLED_CONFIG}" 600 '安裝後的設定'
require_mode "${FAKE_HOME}/.config/jobfinder" 700 '安裝後的設定目錄'

api_token="$(config_value token)"
[[ -n "${api_token}" && "${api_token}" != "CHANGE_ME" ]] || fail "token 未生成"
[[ "${#api_token}" -ge 32 ]] || fail "token 長度僅 ${#api_token}，不足以視為隨機生成"

paths_out="${DEPLOY_ROOT}/d1-paths.txt"
env HOME="${FAKE_HOME}" XDG_CONFIG_HOME="${FAKE_HOME}/.config" \
  XDG_DATA_HOME="${FAKE_HOME}/.local/share" \
  "${INSTALLED_BIN}" paths > "${paths_out}" 2>&1 || fail "jobfinder paths 執行失敗"
if grep -oE '/[^ ]*jobfinder[^ ]*' "${paths_out}" | grep -qv "^${FAKE_HOME}"; then
  fail "jobfinder paths 印出安裝根之外的位置"
fi

assert_contains "${SYSTEMCTL_LOG}" 'daemon-reload' "install 未觸發 daemon-reload"
assert_contains "${SYSTEMCTL_LOG}" 'enable jobfinder-api.service jobfinder-run.timer' \
  "install 未啟用 API service 與 run timer"
for unit in jobfinder-api.service jobfinder-run.service jobfinder-run.timer; do
  [[ -f "${INSTALLED_UNITS}/${unit}" ]] || fail "unit ${unit} 未渲染到 ${INSTALLED_UNITS}"
done
# Only the two services command the binary; the timer just names the service it
# triggers, so it carries no path to check.
for unit in jobfinder-api.service jobfinder-run.service; do
  grep -qF "${INSTALLED_BIN}" "${INSTALLED_UNITS}/${unit}" \
    || fail "unit ${unit} 未指向安裝出的 binary"
  grep -qF "${INSTALLED_CONFIG}" "${INSTALLED_UNITS}/${unit}" \
    || fail "unit ${unit} 未指向安裝出的設定"
done
grep -qF 'Unit=jobfinder-run.service' "${INSTALLED_UNITS}/jobfinder-run.timer" \
  || fail "run timer 未指向 jobfinder-run.service"

record "- 觀察：binary、設定、三個 unit 全部落在隔離根內；token 長度 ${#api_token}；設定 0600"

# Three settings in the rendered config cannot stand in an isolated run. The
# API port belongs to whatever real install this machine already has; the
# resident worker would reach for agent CLIs this environment has no reason to
# provide; and the source must answer on loopback rather than the live site.
sed -i "s|^  addr: .*|  addr: 127.0.0.1:${DEPLOY_PORT}|" "${INSTALLED_CONFIG}"
sed -i "s|^  paused: false|  paused: true|" "${INSTALLED_CONFIG}"
sed -i "s|^    enabled: true|    enabled: true\n    base_url: http://127.0.0.1:${MOCK_SOURCE_PORT}|" \
  "${INSTALLED_CONFIG}"
sed -i "s|^    request_delay_min: .*|    request_delay_min: 0s|" "${INSTALLED_CONFIG}"
sed -i "s|^    request_delay_max: .*|    request_delay_max: 0s|" "${INSTALLED_CONFIG}"
sed -i "s|^    max_pages: .*|    max_pages: 1|" "${INSTALLED_CONFIG}"

api_unit="jobfinder-deploy-api-$$"
systemd-run --user --collect --quiet --unit="${api_unit}" \
  --property=Type=simple \
  --setenv="HOME=${FAKE_HOME}" \
  --setenv="XDG_CONFIG_HOME=${FAKE_HOME}/.config" \
  --setenv="XDG_DATA_HOME=${FAKE_HOME}/.local/share" \
  "${INSTALLED_BIN}" serve --config "${INSTALLED_CONFIG}" \
  || fail "transient API 單元啟動失敗"

wait_for_api || fail "API 在 20 秒內未回應 200"
unauth="$(api_status /api/v1/jobs)"
[[ "${unauth}" == "401" ]] || fail "未帶 token 的請求回 ${unauth}，預期 401"

record "- 觀察：帶 token 回 200、未帶 token 回 401（API 生效面）"
pass_step

# ---------------------------------------------------------------- D2

step D2 "既有設定不覆寫"
record "- 動作：改動設定後重跑 install"

sed -i "s|^  extension_origin: .*|  extension_origin: chrome-extension://deployverifyfixedidaaaaaaaaaaaaa|" \
  "${INSTALLED_CONFIG}"
before="$(sha256sum "${INSTALLED_CONFIG}" | cut -d' ' -f1)"

install_as "${BIN_V1}" install > "${DEPLOY_ROOT}/d2-install.log" 2>&1 \
  || fail "重跑 install 失敗，輸出見 ${DEPLOY_ROOT}/d2-install.log"

after="$(sha256sum "${INSTALLED_CONFIG}" | cut -d' ' -f1)"
[[ "${before}" == "${after}" ]] || fail "重跑 install 改動了既有設定"
record "- 觀察：設定 sha256 前後一致（${before:0:12}…），安裝仍成功"
pass_step

# ---------------------------------------------------------------- D3

step D3 "排程實際觸發"
record "- 動作：以 transient one-shot 執行渲染出的 run unit 命令"

# The binary carries its own fixture source, so the fetch exercises the real
# crawler path without reaching the live site.
"${INSTALLED_BIN}" verify mock-source --addr "127.0.0.1:${MOCK_SOURCE_PORT}" \
  > "${DEPLOY_ROOT}/mock-source.log" 2>&1 &
mock_source_pid=$!
# Drop it from the job table so the forced teardown in cleanup does not print a
# job-control notice over the answer sheet's closing lines.
disown "${mock_source_pid}" 2>/dev/null || true
for _ in $(seq 1 40); do
  curl --fail --silent "http://127.0.0.1:${MOCK_SOURCE_PORT}/healthz" >/dev/null && break
  sleep 0.5
done
curl --fail --silent "http://127.0.0.1:${MOCK_SOURCE_PORT}/healthz" >/dev/null \
  || environment_blocked "fixture 來源伺服器未能在 20 秒內就緒"

timer_unit="jobfinder-deploy-run-$$"
if systemd-run --user --wait --pipe --collect --quiet --unit="${timer_unit}" \
  --setenv="HOME=${FAKE_HOME}" \
  --setenv="XDG_CONFIG_HOME=${FAKE_HOME}/.config" \
  --setenv="XDG_DATA_HOME=${FAKE_HOME}/.local/share" \
  "${INSTALLED_BIN}" run --config "${INSTALLED_CONFIG}" --trigger timer \
  > "${DEPLOY_ROOT}/d3-run.log" 2>&1; then
  runs_json="$(curl -s -H "Authorization: Bearer ${api_token}" \
    "http://127.0.0.1:${DEPLOY_PORT}/api/v1/runs" || printf '')"
  printf '%s' "${runs_json}" | grep -q '"trigger":"timer"' \
    || fail "runs 內找不到 trigger 為 timer 的紀錄；回應：${runs_json:0:200}"
  record "- 觀察：one-shot 成功結束，runs 新增一筆 trigger=timer"
  pass_step
else
  fail "transient one-shot 執行失敗，輸出見 ${DEPLOY_ROOT}/d3-run.log"
fi

# ---------------------------------------------------------------- D5

step D5 "更新確實生效"
record "- 動作：對新版工件執行 update"

install_as "${BIN_V2}" update > "${DEPLOY_ROOT}/d5-update.log" 2>&1 \
  || fail "update 失敗，輸出見 ${DEPLOY_ROOT}/d5-update.log"

version_now="$("${INSTALLED_BIN}" version)"
printf '%s' "${version_now}" | grep -q 'v0.0.2-deploy-b' \
  || fail "更新後版號為 ${version_now}，預期 v0.0.2-deploy-b"
[[ -f "${FAKE_HOME}/.local/lib/jobfinder/jobfinder.prev" ]] \
  || fail "update 未保留回滾工件 jobfinder.prev"
assert_contains "${SYSTEMCTL_LOG}" 'restart jobfinder-api.service' "update 未要求重啟 API service"
record "- 觀察：版號為 ${version_now}；jobfinder.prev 已保留"
pass_step

# ---------------------------------------------------------------- D5A

step D5A "更新不依賴服務當下是否在跑"
record "- 動作：同一份工件再跑一次 update"

prev_before="$(sha256sum "${FAKE_HOME}/.local/lib/jobfinder/jobfinder.prev" | cut -d' ' -f1)"
install_as "${BIN_V2}" update > "${DEPLOY_ROOT}/d5a-update.log" 2>&1 \
  || fail "重跑 update 失敗，輸出見 ${DEPLOY_ROOT}/d5a-update.log"
prev_after="$(sha256sum "${FAKE_HOME}/.local/lib/jobfinder/jobfinder.prev" | cut -d' ' -f1)"
[[ "${prev_before}" == "${prev_after}" ]] \
  || fail "以同一份工件重跑 update 後，jobfinder.prev 不再是前一版"
record "- 觀察：jobfinder.prev 仍為前一版（${prev_before:0:12}…）"
pass_step

# ---------------------------------------------------------------- D6

step D6 "回滾"
record "- 動作：jobfinder rollback"

db_before="$(sha256sum "${INSTALLED_DB}" | cut -d' ' -f1)"
install_as "${INSTALLED_BIN}" rollback > "${DEPLOY_ROOT}/d6-rollback.log" 2>&1 \
  || fail "rollback 失敗，輸出見 ${DEPLOY_ROOT}/d6-rollback.log"

version_back="$("${INSTALLED_BIN}" version)"
printf '%s' "${version_back}" | grep -q 'v0.0.1-deploy-a' \
  || fail "回滾後版號為 ${version_back}，預期 v0.0.1-deploy-a"
db_after="$(sha256sum "${INSTALLED_DB}" | cut -d' ' -f1)"
[[ "${db_before}" == "${db_after}" ]] || fail "rollback 動到了資料庫"
[[ -f "${FAKE_HOME}/.local/lib/jobfinder/jobfinder.bad" ]] \
  || fail "rollback 未把被撤下的版本保留為 .bad"
record "- 觀察：版號回到 ${version_back}；資料庫 sha256 未變；.bad 已保留"
pass_step

# ---------------------------------------------------------------- D6B

step D6B "回滾只退一版"
record "- 動作：回滾後再執行一次 rollback"

bad_before="$(sha256sum "${FAKE_HOME}/.local/lib/jobfinder/jobfinder.bad" | cut -d' ' -f1)"
bin_before="$(sha256sum "${INSTALLED_BIN}" | cut -d' ' -f1)"
if install_as "${INSTALLED_BIN}" rollback > "${DEPLOY_ROOT}/d6b-rollback.log" 2>&1; then
  fail "第二次 rollback 應被拒絕，實際成功"
fi
bad_after="$(sha256sum "${FAKE_HOME}/.local/lib/jobfinder/jobfinder.bad" | cut -d' ' -f1)"
bin_after="$(sha256sum "${INSTALLED_BIN}" | cut -d' ' -f1)"
[[ "${bad_before}" == "${bad_after}" ]] || fail "被拒絕的 rollback 仍動了 .bad"
[[ "${bin_before}" == "${bin_after}" ]] || fail "被拒絕的 rollback 仍動了常駐 binary"
record "- 觀察：第二次被拒絕，.bad 與常駐 binary 皆未變動"
pass_step

# ---------------------------------------------------------------- 收尾

record ''
record '## 結果'
record "- PASS：${pass_count}"
record "- 未涵蓋：D4／D7／D8／D9（人工組）、D10（待 repo 轉 public）"
record "- 清理：離開時停止 transient 單元與 fixture 伺服器，並刪除隔離根 ${DEPLOY_ROOT}"

printf '\n==> 部署驗收自動組 PASS %s 項\n' "${pass_count}"
printf '==> 答案卷：%s\n' "${report}"
