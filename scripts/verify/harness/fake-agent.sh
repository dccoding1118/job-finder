#!/usr/bin/env bash

set -euo pipefail

prompt="${!#}"
case "${prompt}" in
  *"評分器"*)
    if [[ "${prompt}" == *"Verification low score"* ]]; then
      printf '%s\n' '{"hard_skill":60,"domain":60,"seniority":60,"condition":60,"direction":60,"reason":"合成低分情境"}'
    elif [[ "${prompt}" == *"Verification failure"* ]]; then
      printf '%s\n' '{"hard_skill":80,"domain":80,"seniority":80,"condition":80,"direction":80,"reason":"合成重試情境"}'
    else
      printf '%s\n' '{"hard_skill":90,"domain":90,"seniority":90,"condition":90,"direction":90,"reason":"合成核准情境"}'
    fi
    ;;
  *"起草器"*)
    printf '%s\n' '{"letter":"我使用 Go 建立可靠服務。[你的姓名][你的聯絡方式]"}'
    ;;
  *"審查器"*)
    if [[ "${prompt}" == *"Verification failure"* ]]; then
      printf '%s\n' '{"verdict":"revise","issues":["驗證用的固定退回結果"]}'
    else
      printf '%s\n' '{"verdict":"approve","issues":[]}'
    fi
    ;;
  *)
    printf 'unknown fake-agent prompt\n' >&2
    exit 64
    ;;
esac
