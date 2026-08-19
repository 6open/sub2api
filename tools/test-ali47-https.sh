#!/usr/bin/env bash

set -u

HOST="47-99-93-199.sslip.io"
BASE_URL="https://${HOST}/v1"
MODEL="${MODEL:-gpt-5.6-luna}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "== ali47 HTTPS connectivity test =="
echo "Time: $(date '+%Y-%m-%d %H:%M:%S %z')"
echo "Base URL: $BASE_URL"
echo

echo "[1/3] DNS"
if command -v getent >/dev/null 2>&1; then
  getent ahostsv4 "$HOST" | head -n 1 || true
elif command -v nslookup >/dev/null 2>&1; then
  nslookup "$HOST" || true
else
  echo "DNS tool not found; continuing with curl."
fi
echo

echo "[2/3] HTTPS health check"
HEALTH_CODE="$(curl --noproxy '*' -sS \
  --connect-timeout 10 --max-time 20 \
  -o "$TMP_DIR/health.body" -w '%{http_code}' \
  "https://${HOST}/health" 2>"$TMP_DIR/health.error")"
HEALTH_RC=$?

if [ "$HEALTH_RC" -ne 0 ]; then
  echo "FAIL: curl exit code $HEALTH_RC"
  cat "$TMP_DIR/health.error"
  exit 1
fi

echo "HTTP $HEALTH_CODE: $(cat "$TMP_DIR/health.body")"
if [ "$HEALTH_CODE" != "200" ]; then
  echo "FAIL: health check did not return HTTP 200."
  exit 1
fi
echo

echo "[3/3] Responses API streaming test"
if [ -z "${SUB2API_KEY:-}" ]; then
  read -r -s -p "API Key: " SUB2API_KEY
  echo
fi

START_TIME="$(date +%s)"
curl --noproxy '*' -sS -N \
  --connect-timeout 10 --max-time 120 \
  -D "$TMP_DIR/api.headers" \
  -o "$TMP_DIR/api.body" \
  -H "Authorization: Bearer ${SUB2API_KEY}" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"${MODEL}\",\"input\":\"Reply with OK only.\",\"stream\":true}" \
  "${BASE_URL}/responses" 2>"$TMP_DIR/api.error"
API_RC=$?
ELAPSED="$(( $(date +%s) - START_TIME ))"
unset SUB2API_KEY

HTTP_STATUS="$(awk 'toupper($1) ~ /^HTTP\// {code=$2} END {print code}' "$TMP_DIR/api.headers")"
REQUEST_ID="$(awk -F ': ' 'tolower($1) == "x-request-id" {gsub("\r", "", $2); print $2}' "$TMP_DIR/api.headers" | tail -n 1)"

echo "HTTP: ${HTTP_STATUS:-unknown}"
echo "Elapsed: ${ELAPSED}s"
echo "Request ID: ${REQUEST_ID:-none}"

if [ "$API_RC" -ne 0 ]; then
  echo "FAIL: curl exit code $API_RC"
  cat "$TMP_DIR/api.error"
  head -c 1000 "$TMP_DIR/api.body"
  echo
  exit 1
fi

if [ "$HTTP_STATUS" != "200" ]; then
  echo "FAIL: API did not return HTTP 200."
  head -c 1000 "$TMP_DIR/api.body"
  echo
  exit 1
fi

if ! grep -q 'event: response.completed' "$TMP_DIR/api.body"; then
  echo "FAIL: stream ended without response.completed."
  tail -n 20 "$TMP_DIR/api.body"
  exit 1
fi

echo "PASS: HTTPS and streaming Responses API are working."
