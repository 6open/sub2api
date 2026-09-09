#!/bin/sh
set -eu
nc -lk -p 5432 -e /relay/pg-relay.sh &
relay_pid=$!
trap 'kill "$relay_pid" 2>/dev/null || true' EXIT INT TERM
nc -lk -p 6379 -e /relay/redis-relay.sh
