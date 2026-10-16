#!/usr/bin/env bash
set -euo pipefail
set +x
umask 077
cd "$(dirname "$0")/.."
unset TF_LOG TF_LOG_PATH YC_TOKEN
if ! YC_TOKEN=$(yc iam create-token 2>/dev/null); then
    unset YC_TOKEN
    printf 'Не удалось получить IAM-токен\n' >&2
    exit 1
fi
test -n "$YC_TOKEN" || exit 1
export YC_TOKEN
trap 'unset YC_TOKEN' EXIT
python3 scripts/terraform_run.py "$@"
