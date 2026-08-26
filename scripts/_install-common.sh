#!/usr/bin/env bash
# Общая часть install-service.sh и install-from-dist.sh: раскладывает уже
# собранный бинарник, конфиг и systemd unit. Не предназначен для
# самостоятельного запуска - подключается через `source`.

BIN_NAME=mtproto-proxy
BIN_PATH=/usr/local/bin/$BIN_NAME
CONFIG_DIR=/etc/mtproto-proxy
CONFIG_PATH=$CONFIG_DIR/config.json
UNIT_PATH=/etc/systemd/system/$BIN_NAME.service

require_root() {
    if [[ $EUID -ne 0 ]]; then
        echo "Запустите скрипт от root (sudo $0)" >&2
        exit 1
    fi
}

install_service_common() {
    local repo_root=$1
    local built_bin=$2

    echo "==> Установка бинарника в $BIN_PATH"
    install -m 0755 "$built_bin" "$BIN_PATH"

    echo "==> Подготовка $CONFIG_DIR"
    mkdir -p "$CONFIG_DIR"
    if [[ ! -s "$CONFIG_PATH" ]]; then
        # Файла нет либо он пустой (0 байт) - можно спокойно засеять примером.
        install -m 0644 "$repo_root/config.example.json" "$CONFIG_PATH"
        echo "    создан $CONFIG_PATH из config.example.json"
    elif [[ "$(grep -c '"secret"[[:space:]]*:' "$CONFIG_PATH" || true)" -eq 1 ]] && \
         grep -q '"name"[[:space:]]*:[[:space:]]*"default"' "$CONFIG_PATH"; then
        # Файл существует, но в нем ровно один секрет с именем "default" - это
        # заглушка, которую сам mtproto-proxy генерирует и сохраняет, если
        # запустить его с -config на файл без единого секрета (см.
        # buildSecrets/EffectiveSecrets в internal/mtproto/server.go), а не
        # осознанно настроенный конфиг. Сохраняем такой файл на всякий случай
        # и засеваем заново из примера.
        local backup="$CONFIG_PATH.bak.$(date +%Y%m%d%H%M%S)"
        cp -p "$CONFIG_PATH" "$backup"
        install -m 0644 "$repo_root/config.example.json" "$CONFIG_PATH"
        echo "    $CONFIG_PATH содержал только авто-сгенерированный секрет 'default';"
        echo "    старый файл сохранен как $backup, конфиг пересоздан из config.example.json"
    else
        echo "    $CONFIG_PATH уже существует и содержит секреты, не трогаю"
    fi
    chown -R nobody:nogroup "$CONFIG_DIR"

    echo "==> Установка unit-файла в $UNIT_PATH"
    install -m 0644 "$repo_root/mtproto-proxy.service.example" "$UNIT_PATH"

    echo "==> systemctl daemon-reload && enable --now $BIN_NAME"
    systemctl daemon-reload
    systemctl enable --now "$BIN_NAME"

    echo "==> Готово. Статус:"
    systemctl --no-pager status "$BIN_NAME" || true

    echo
    echo "==> Секреты, которые реально использует запущенный сервис:"
    sleep 1
    journalctl -u "$BIN_NAME" --no-pager -n 200 | grep -A1 -E '^\s*[A-Za-z0-9_.-]+:$' | grep -B1 'secret:' || \
        echo "    (не нашел в логе - смотрите: journalctl -u $BIN_NAME -n 200)"
}
