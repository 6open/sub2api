#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC="$ROOT/tools/lklb-code-shop/app_stdlib.py"
REMOTE="ali98"
TARGET="/opt/lklb-code-shop/app_stdlib.py"
ssh "$REMOTE" "cp '$TARGET' '$TARGET.bak.'\$(date +%Y%m%d%H%M%S)"
scp "$SRC" "$REMOTE:$TARGET"
ssh "$REMOTE" "sudo -n systemctl restart lklb-code-shop && systemctl is-active lklb-code-shop"
