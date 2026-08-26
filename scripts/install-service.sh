#!/usr/bin/env bash
# Собирает mtproto-proxy и устанавливает его как systemd-сервис.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$REPO_ROOT/scripts/_install-common.sh"

require_root

if ! command -v go >/dev/null 2>&1; then
    for candidate in /usr/local/go/bin /usr/lib/go/bin; do
        if [[ -x "$candidate/go" ]]; then
            export PATH="$candidate:$PATH"
            break
        fi
    done
fi

if ! command -v go >/dev/null 2>&1; then
    echo "go не найден в PATH (sudo обычно сбрасывает PATH вызывающего пользователя)." >&2
    echo "Установите Go или запустите: sudo env PATH=\"\$PATH\" $0" >&2
    exit 1
fi

echo "==> Сборка $BIN_NAME"
make -C "$REPO_ROOT" build

install_service_common "$REPO_ROOT" "$REPO_ROOT/dist/$BIN_NAME"
