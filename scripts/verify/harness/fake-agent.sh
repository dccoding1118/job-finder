#!/usr/bin/env bash

# Stands in for the headless claude and codex CLIs. The installed basename selects
# the interface: claude reads the prompt from stdin and prints a JSON result
# envelope; codex takes the prompt as its final argument and writes its final
# message to the file given by -o.

set -euo pipefail

agent="$(basename "$0")"
last_message=''

case "${agent}" in
  claude)
    prompt="$(cat)"
    ;;
  codex)
    prompt="${!#}"
    args=("$@")
    for ((i = 0; i < ${#args[@]} - 1; i++)); do
      if [[ "${args[i]}" == '-o' ]]; then
        last_message="${args[i + 1]}"
      fi
    done
    [[ -n "${last_message}" ]] || {
      printf 'fake codex requires -o <file>\n' >&2
      exit 64
    }
    ;;
  *)
    printf 'fake agent installed under an unsupported name: %s\n' "${agent}" >&2
    exit 64
    ;;
esac

case "${prompt}" in
  # The screening gate answers with the JD broken into conditions. The fixture
  # descriptions drive which of the three outcomes the run exercises.
  *"硬條件篩選器"*)
    if [[ "${prompt}" == *"Verification unknown"* ]]; then
      payload='{"conditions":[{"text":"需相關證照","kind":"required","group":1,"category":"certification","verdict":"unknown","years_required":null,"years_max":null,"industry_keys":[]}]}'
    elif [[ "${prompt}" == *"Verification unfit"* ]]; then
      payload='{"conditions":[{"text":"需具備未持有的必備技能","kind":"required","group":1,"category":"skill","verdict":"fail","years_required":null,"years_max":null,"industry_keys":[]}]}'
    else
      payload='{"conditions":[{"text":"熟悉 Go 與雲端平台","kind":"required","group":1,"category":"skill","verdict":"pass","years_required":null,"years_max":null,"industry_keys":[]},{"text":"有 Kubernetes 經驗尤佳","kind":"bonus","group":2,"category":"skill","verdict":"fail","years_required":null,"years_max":null,"industry_keys":[]}]}'
    fi
    ;;
  *"評分器"*)
	if [[ -n "${JOBFINDER_VERIFY_AGENT_SIGNAL:-}" ]]; then
	  : >"${JOBFINDER_VERIFY_AGENT_SIGNAL}"
	fi
	if [[ -n "${JOBFINDER_VERIFY_AGENT_DELAY:-}" ]]; then
	  sleep "${JOBFINDER_VERIFY_AGENT_DELAY}"
	fi
    if [[ "${prompt}" == *"Verification low score"* ]]; then
      payload='{"content_fit":60,"benefit_fit":60,"bonus_fit":60,"industry_fit":60,"reason":"合成低分情境"}'
    elif [[ "${prompt}" == *"Verification unknown"* ]]; then
      payload='{"content_fit":70,"benefit_fit":70,"bonus_fit":70,"industry_fit":70,"reason":"合成資訊不足情境"}'
    elif [[ "${prompt}" == *"Verification failure"* ]]; then
      payload='{"content_fit":80,"benefit_fit":80,"bonus_fit":80,"industry_fit":80,"reason":"合成重試情境"}'
    else
      payload='{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"合成核准情境"}'
    fi
    ;;
  *"起草器"*)
    payload='{"letter":"我使用 Go 建立可靠服務。[你的姓名][你的聯絡方式]"}'
    ;;
  *"審查器"*)
    # The failure fixture answers with prose instead of a JSON object, so every
    # reviewer runner is exhausted and the job takes the failed-call path.
    if [[ "${prompt}" == *"Verification failure"* ]]; then
      payload='驗證用的固定不合法回應'
    else
      payload='{"verdict":"approve","issues":[]}'
    fi
    ;;
  *)
    printf 'unknown fake-agent prompt\n' >&2
    exit 64
    ;;
esac

case "${agent}" in
  claude)
    escaped="${payload//\\/\\\\}"
    escaped="${escaped//\"/\\\"}"
    # The real CLI reports its own token accounting and cost on the same
    # envelope; fixed synthetic values keep the daily usage view assertable.
    printf '{"type":"result","subtype":"success","is_error":false,"result":"%s","total_cost_usd":0.0125,"usage":{"input_tokens":1200,"output_tokens":150,"cache_read_input_tokens":800,"cache_creation_input_tokens":40}}\n' "${escaped}"
    ;;
  codex)
    printf '%s\n' "${payload}" >"${last_message}"
    printf 'fake codex transcript; final message written to %s\n' "${last_message}"
    ;;
esac
