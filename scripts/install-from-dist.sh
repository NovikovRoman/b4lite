#!/usr/bin/env bash
# Устанавливает mtproto-proxy как systemd-сервис из уже собранного
# dist/mtproto-proxy (см. `make build`), не запуская сборку заново.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$REPO_ROOT/scripts/_install-common.sh"

require_root

BUILT_BIN="$REPO_ROOT/dist/$BIN_NAME"
if [[ ! -x "$BUILT_BIN" ]]; then
    echo "Не найден $BUILT_BIN - сначала соберите: make build" >&2
    exit 1
fi

install_service_common "$REPO_ROOT" "$BUILT_BIN"
