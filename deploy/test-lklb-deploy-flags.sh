#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

set +e
output="$(./deploy/lklb-deploy-ali98.sh --backend-fast --help 2>&1)"
status=$?
set -e

if [[ "$status" -ne 0 ]]; then
  echo "expected --backend-fast --help to exit 0, got $status" >&2
  echo "$output" >&2
  exit 1
fi

if ! grep -q -- '--backend-fast' <<<"$output"; then
  echo "expected --backend-fast to be documented in help output" >&2
  echo "$output" >&2
  exit 1
fi

if ! grep -q -- 'GO_BACKEND_TEST_CMD' <<<"$output"; then
  echo "expected GO_BACKEND_TEST_CMD to be documented in help output" >&2
  echo "$output" >&2
  exit 1
fi

if ! grep -q '^docker_go()' deploy/lklb-deploy-ali98.sh; then
  echo "expected deploy script to use a shared docker_go helper" >&2
  exit 1
fi

if [[ "$(grep -c -- '--network host' deploy/lklb-deploy-ali98.sh)" -ne 1 ]]; then
  echo "expected docker --network host to be defined once in shared helper" >&2
  exit 1
fi

if [[ "$(grep -c -- '-e GOPROXY=' deploy/lklb-deploy-ali98.sh)" -ne 1 ]]; then
  echo "expected GOPROXY docker env to be defined once in shared helper" >&2
  exit 1
fi

if [[ "$(grep -c -- '-e GOCACHE=/go-build-cache' deploy/lklb-deploy-ali98.sh)" -ne 1 ]]; then
  echo "expected GOCACHE docker env to be defined once in shared helper" >&2
  exit 1
fi

if [[ "$(grep -c -- '-e GOMODCACHE=/go-mod-cache' deploy/lklb-deploy-ali98.sh)" -ne 1 ]]; then
  echo "expected GOMODCACHE docker env to be defined once in shared helper" >&2
  exit 1
fi

echo "deploy flag tests passed"
