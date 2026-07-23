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
  *"評分器"*)
	if [[ -n "${JOBFINDER_VERIFY_AGENT_SIGNAL:-}" ]]; then
	  : >"${JOBFINDER_VERIFY_AGENT_SIGNAL}"
	fi
	if [[ -n "${JOBFINDER_VERIFY_AGENT_DELAY:-}" ]]; then
	  sleep "${JOBFINDER_VERIFY_AGENT_DELAY}"
	fi
    if [[ "${prompt}" == *"Verification low score"* ]]; then
      payload='{"hard_skill":60,"domain":60,"seniority":60,"condition":60,"direction":60,"reason":"合成低分情境"}'
    elif [[ "${prompt}" == *"Verification failure"* ]]; then
      payload='{"hard_skill":80,"domain":80,"seniority":80,"condition":80,"direction":80,"reason":"合成重試情境"}'
    else
      payload='{"hard_skill":90,"domain":90,"seniority":90,"condition":90,"direction":90,"reason":"合成核准情境"}'
    fi
    ;;
  *"起草器"*)
    payload='{"letter":"我使用 Go 建立可靠服務。[你的姓名][你的聯絡方式]"}'
    ;;
  *"審查器"*)
    if [[ "${prompt}" == *"Verification failure"* ]]; then
      payload='{"verdict":"revise","issues":["驗證用的固定退回結果"]}'
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
    printf '{"type":"result","subtype":"success","is_error":false,"result":"%s"}\n' "${escaped}"
    ;;
  codex)
    printf '%s\n' "${payload}" >"${last_message}"
    printf 'fake codex transcript; final message written to %s\n' "${last_message}"
    ;;
esac
