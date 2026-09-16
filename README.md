# mtproto-proxy

Автономный MTProto (Telegram) прокси-сервер, выделенный из функции
"MTProto Proxy" проекта [b4](https://github.com/DanielLavrushin/b4).

Это fake-TLS прокси: клиенты Telegram подключаются к нему напрямую (по
умолчанию на порт 3128), рукопожатие (handshake) выглядит как обычное TLS
соединение к настоящему на вид домену (по умолчанию `storage.googleapis.com`),
а сервер перенаправляет MTProto-трафик в нужный дата-центр Telegram.
Никакого движка обхода DPI, netfilter-очередей или
root-привилегий для запуска не требуется - это просто TCP-прокси.

Смотрите `NOTICE.md`, чтобы узнать, что именно было скопировано из b4, а
что написано с нуля.

## Сборка

Требуется Go 1.22+.

```sh
make
```

или

```sh
go build -o mtproto-proxy ./cmd/mtproto-proxy
```

## Запуск

```sh
# первый запуск: генерирует секрет, слушает 0.0.0.0:3128, печатает `tg://` ссылку
./mtproto-proxy

# явно задать хост/порт и сохранять состояние (сгенерированный секрет) в файл
./mtproto-proxy -config /etc/mtproto-proxy/config.json -port 8443

# использовать свой секрет (32 hex-байта, "ee" + 16-байтный ключ + hostname, в hex)
./mtproto-proxy -secret ee0102...

# сгенерировать секрет только для конкретного домена-прикрытия
./mtproto-proxy -secret-host www.cloudflare.com
```

Флаги:

| Флаг           | Значение                                                          |
|----------------|-------------------------------------------------------------------|
| `-config`      | JSON-файл конфигурации; создается/обновляется автоматически (в нем хранятся автогенерируемые секреты, чтобы они сохранялись между перезапусками) |
| `-bind`        | адрес для прослушивания (по умолчанию `0.0.0.0`)                   |
| `-port`        | порт для прослушивания (по умолчанию `3128`)                       |
| `-fake-sni`    | домен, под который маскируется fake-TLS рукопожатие (по умолчанию `storage.googleapis.com`) |
| `-upstream`    | `auto` \| `ws` \| `tcp` - как обращаться к дата-центрам Telegram   |
| `-secret`      | добавить точный hex-секрет; флаг можно повторять несколько раз     |
| `-secret-host` | автоматически сгенерировать секрет для указанного домена-прикрытия; флаг можно повторять несколько раз |
| `-v`           | уровень логирования: `0` ошибки, `1` инфо (по умолчанию), `2` трассировка, `3` отладка |

При запуске для каждого настроенного секрета печатается ссылка вида
`tg://proxy?...` / `https://t.me/proxy?...` - перед тем как делиться
ими, подставьте в них реальный публичный IP-адрес или хостнейм вашего
сервера.

### Несколько секретов

Сервер поддерживает произвольное количество секретов одновременно,
каждый из которых работает как независимо отзываемый "логин" (Telegram
различает клиентов по тому, каким секретом они воспользовались, так что
можно выдать по одному секрету на устройство/человека, а затем отозвать
именно этот секрет, не затронув остальные). Все способы настройки из
исходного кода b4 сохранены:

```sh
# повторяйте -secret, при желании в формате "ИМЯ=HEXSECRET" для подписи каждого
./mtproto-proxy -secret "phone=ee0102..." -secret "laptop=ee1112..."

# либо повторяйте -secret-host, чтобы сгенерировать по одному секрету на каждый домен-прикрытие
./mtproto-proxy -secret-host www.cloudflare.com -secret-host www.microsoft.com
```

либо перечислите их прямо в конфигурационном файле - это более удобный
способ управлять большим количеством секретов (например, по одному на
человека) - переключайте `enabled`, чтобы отозвать секрет, не удаляя его:

```json
{
  "system": {
    "mtproto": {
      "port": 3128,
      "secrets": [
        {"id": "phone",  "name": "phone",  "secret": "ee0102...", "enabled": true, "max_networks": 2},
        {"id": "laptop", "name": "laptop", "secret": "ee1112...", "enabled": true},
        {"id": "old",    "name": "old",    "secret": "ee2122...", "enabled": false}
      ]
    }
  }
}
```

Флаги `-secret`/`-secret-host`, если они заданы, полностью заменяют
содержимое конфигурационного файла, а не объединяются с ним.

`max_networks` ограничивает секрет сверху числом одновременно
используемых сетей (один IPv4-адрес или один IPv6 /64 считаются одной
сетью, так что смена адреса у одного и того же клиента не съедает
лимит) - `0` (по умолчанию) означает "без ограничения". Подключение
сверх лимита отклоняется, а не вытесняет уже работающих клиентов, так
что случайно (или намеренно) переданный кому-то ещё секрет стоит
лишнего отказа в соединении, а не обрыва чужой сессии. Текущее число
занятых сетей на секрет видно в `Server.Stats()`.

## Пример полного config.json

Ниже - пример конфигурационного файла со всеми доступными полями (не
все из них обязательны; отсутствующие поля просто принимают значения по
умолчанию). Такой же файл лежит в репозитории как
[`config.example.json`](config.example.json).

```json
{
  "version": 1,
  "queue": {
    "ipv4": true,
    "ipv6": true
  },
  "system": {
    "mtproto": {
      "enabled": true,
      "port": 3128,
      "bind_address": "0.0.0.0",
      "max_connections": 2048,
      "tcp_user_timeout_sec": 0,
      "idle_timeout_sec": 0,
      "bridge_wait_sec": 0,
      "secrets": [
        {"id": "phone",  "name": "phone",  "secret": "ee0102030405060708090a0b0c0d0e0f10777777772e676f6f676c652e636f6d", "enabled": true},
        {"id": "laptop", "name": "laptop", "secret": "ee1112131415161718191a1b1c1d1e1f20777777772e636c6f7564666c6172652e636f6d", "enabled": true},
        {"id": "old",    "name": "old",    "secret": "ee2122232425262728292a2b2c2d2e2f30777777772e6d6963726f736f66742e636f6d", "enabled": false}
      ],
      "fake_sni": "storage.googleapis.com",
      "dc_relay": "",
      "upstream_mode": "auto",
      "ws_custom_domain": "",
      "ws_endpoint_host": "149.154.167.220",
      "cfproxy_enabled": true,
      "cfproxy_url": "https://raw.githubusercontent.com/Flowseal/tg-ws-proxy/main/.github/cfproxy-domains.txt",
      "cfworker_domain": "",
      "dc_fallback_enabled": true,
      "dc_fallback_url": "https://proxy.lavrush.in/telegram/getProxyConfig",
      "web_proxy": {
        "enabled": false,
        "hostname": ""
      }
    }
  }
}
```

Пояснения к полям:

| Поле                                    | Назначение                                                                                     |
|------------------------------------------|-------------------------------------------------------------------------------------------------|
| `version`                                | версия схемы конфига; оставлена для обратной совместимости при будущем расширении формата        |
| `queue.ipv4` / `queue.ipv6`               | учитывать ли A/AAAA-адреса при резолвинге дата-центров Telegram                                  |
| `system.mtproto.enabled`                  | включен ли MTProto-сервер                                                                        |
| `system.mtproto.port`                     | порт для прослушивания                                                                           |
| `system.mtproto.bind_address`             | адрес для прослушивания                                                                          |
| `system.mtproto.max_connections`          | максимум одновременных соединений (`0` = значение по умолчанию, 2048)                            |
| `system.mtproto.tcp_user_timeout_sec`     | значение `TCP_USER_TIMEOUT` в секундах (`0` = не задавать)                                       |
| `system.mtproto.idle_timeout_sec`         | таймаут простаивающего соединения в секундах (`0` = не задавать)                                 |
| `system.mtproto.bridge_wait_sec`          | сколько секунд ждать установления WebSocket/мостового соединения                                 |
| `system.mtproto.secrets`                  | список секретов (см. раздел "Несколько секретов" выше); `enabled: false` отзывает секрет без удаления |
| `system.mtproto.secrets[].max_networks`   | лимит одновременно используемых секретом сетей (`0` = без ограничения); подключение сверх лимита отклоняется |
| `system.mtproto.fake_sni`                 | домен, под который маскируется fake-TLS рукопожатие                                              |
| `system.mtproto.dc_relay`                 | адрес релея дата-центра (если требуется нестандартная маршрутизация)                              |
| `system.mtproto.upstream_mode`            | `auto` \| `ws` \| `tcp` - способ обращения к дата-центрам Telegram                                |
| `system.mtproto.ws_custom_domain`         | пользовательский домен для WebSocket-подключения к Telegram                                      |
| `system.mtproto.ws_endpoint_host`         | IP-адрес WebSocket-эндпоинта Telegram                                                            |
| `system.mtproto.cfproxy_enabled`          | использовать ли резервные Cloudflare Worker-домены при недоступности прямого подключения          |
| `system.mtproto.cfproxy_url`              | URL, откуда подтягивается список Cloudflare Worker-доменов                                        |
| `system.mtproto.cfworker_domain`          | конкретный домен Cloudflare Worker (если не хотите полагаться на автоматический список)           |
| `system.mtproto.dc_fallback_enabled`      | подтягивать ли резервный список адресов дата-центров с `dc_fallback_url`                          |
| `system.mtproto.dc_fallback_url`          | URL резервного списка адресов дата-центров Telegram                                              |
| `system.mtproto.web_proxy.enabled`        | включить дополнительный HTTPS "веб-прокси" - позволяет браузеру/https-клиенту стартовать сессию поверх обычного TLS на том же порту, вместо fake-TLS рукопожатия |
| `system.mtproto.web_proxy.hostname`       | хостнейм, под которым этот HTTPS "веб-прокси" должен себя предъявлять                             |

## Генерация секретов отдельно

`gen-secret` печатает один или несколько свежих fake-TLS секретов без
запуска сервера - удобно для подключения нового человека/устройства без
перезапуска прокси, а также для скриптов:

```sh
# напечатать один секрет с маскировкой под storage.googleapis.com
./mtproto-proxy gen-secret

# конкретный домен-прикрытие и метка
./mtproto-proxy gen-secret -host www.cloudflare.com -name alice

# сгенерировать сразу пачку секретов
./mtproto-proxy gen-secret -host www.cloudflare.com -count 5

# сгенерировать секрет И сразу дописать его в конфиг-файл, которым
# пользуется запущенный экземпляр (перезапустите его, либо отправьте
# SIGHUP, если вы сами настроили такую обработку, чтобы секрет подхватился)
./mtproto-proxy gen-secret -host www.cloudflare.com -name alice -config /etc/mtproto-proxy/config.json
```

Флаги `gen-secret`:

| Флаг             | Значение                                                                 |
|------------------|--------------------------------------------------------------------------|
| `-host`          | домен-прикрытие, встраиваемый в секрет (по умолчанию `storage.googleapis.com`) |
| `-name`          | метка секрета (по умолчанию - имя хоста, либо `secret-N`/`ИМЯ-N` при `-count > 1`) |
| `-count`         | сколько секретов сгенерировать за раз (по умолчанию `1`)                    |
| `-config`        | дописать сгенерированные секреты в этот JSON-файл конфигурации, а не просто напечатать их |
| `-bind`, `-port` | используются только для построения примеров ссылок, если `-config` *не* задан |

Откройте порт в файрволе (по умолчанию `3128/tcp`) и настройте
проброс, если сервер находится за NAT.

## Установка как systemd-сервис

`scripts/install-service.sh` собирает бинарник, кладет его в
`/usr/local/bin`, создает `/etc/mtproto-proxy/config.json` (из
`config.example.json`, если файла еще нет), устанавливает unit-файл из
`mtproto-proxy.service.example` в `/etc/systemd/system/mtproto-proxy.service`
и запускает сервис через `systemctl enable --now`:

```sh
sudo make install-service
# или напрямую
sudo ./scripts/install-service.sh
```

Если бинарник уже собран (`make build`) и пересобирать не нужно (например,
скрипт запускается из-под root без установленного там Go), используйте
`scripts/install-from-dist.sh` - он берет готовый `dist/mtproto-proxy` и
делает все то же самое, кроме сборки:

```sh
make build
sudo make install-service-from-dist
# или напрямую
sudo ./scripts/install-from-dist.sh
```

Повторный запуск безопасен: существующий `config.json` не
перезаписывается. После правки конфига перезапустите сервис:

```sh
sudo systemctl restart mtproto-proxy
sudo journalctl -u mtproto-proxy -f
```

## Что внутри

```text
cmd/mtproto-proxy/   точка входа: флаги, запуск, обработка сигналов
internal/mtproto/    сам прокси - fake-TLS, obfuscated2-фрейминг,
                      дозвон до дата-центров (напрямую / через WebSocket /
                      с фолбэком через Cloudflare Worker), опциональный
                      HTTPS "веб-прокси"-носитель, релей соединений и статистика
internal/config/     минимальный тип конфига, от которого зависит internal/mtproto
internal/log/        логгер с уровнями, от которого зависит internal/mtproto
internal/leaktest/   вспомогательный модуль для тестов на утечку горутин (только для тестов)
```

## Тесты

Набор тестов скопирован без изменений вместе с тестируемым кодом:

```sh
go test ./...
```

## Лицензия

GPLv3, унаследована от исходного проекта b4 - см. `LICENSE` и `NOTICE.md`.
