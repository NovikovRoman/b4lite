## 1. Tech Stack & Environment

- Модуль `github.com/NovikovRoman/b4lite`; Go **1.27** по `go.mod`. Версии и поведение сверять с исходниками: README местами устарел.
- Один автономный бинарник `mtproto-proxy`: Telegram MTProto, fake-TLS, obfuscated2/AES-CTR, TCP, WebSocket, HTTP(S). Основная платформа — Linux: `x/sys/unix`, `TCP_USER_TIMEOUT`, systemd; DPI/netfilter-движка нет.
- Зависимости: `google/uuid v1.6.0` — ID секретов; `gorilla/websocket v1.5.3` — входящий WEB carrier; `x/sys v0.49.0` — системные вызовы; `uber/goleak v1.3.0` — тесты утечек горутин. Исходящий WS реализован собственным кодом фрейминга/upgrade.
- `make build`: `CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w"`, результат `dist/mtproto-proxy`. Проверки: `make test` / `go test ./...`; для изменений конкурентного кода — `go test -race ./...`.
- Конфигурация — локальный JSON; БД, ORM, внешнего брокера и отдельного frontend-приложения нет. Лицензия GPLv3; происхождение кода — `NOTICE.md`.

## 2. Project Structure

- `cmd/mtproto-proxy/main.go` — CLI: запуск по умолчанию (`mtproto-proxy [flags]`, явной подкоманды `run` нет), подкоманда `gen-secret`, сборка конфигурации, запуск refresh/server, печать ссылок, SIGINT/SIGTERM.
- `internal/config/` — типы JSON, значения по умолчанию, загрузка/сохранение, выбор IP-семейств/IPv6-маршрута; совместимые поля исходного b4 не означают наличие его подсистем.
- `internal/mtproto/` — единый пакет сетевого ядра:
  - `server.go`, `secret.go`, `faketls.go`, `obfuscated2.go` — lifecycle, секреты, входные handshake, лимиты, relay и статистика.
  - `dc.go`, `upstreamrefresh.go`, `dial*.go`, `wsdial.go`, `wspool.go`, `cfproxy.go`, `cfworkerpool.go` — адреса DC, планы/гонки транспортов, TLS/WS dial, предварительно открытые соединения, балансировка/cooldown.
  - `msgsplit.go`, `transporterr.go`, `workerstall.go` — MTProto-фрейминг, обработка ошибок DC, обнаружение зависшего upstream.
  - `webproxy*.go` — отдельный опциональный HTTP(S)-listener, страница/авторизация, WS carrier, мультиплексированные потоки и flow control для Telegram Desktop.
  - `transparent.go`, `bridgedc.go`, `failopen.go` — API прозрачного моста для интеграции; standalone CLI его не подключает.
- `internal/mtproto/httpclient.go` — локальная замена upstream `netprobe.HTTPClient`: HTTP-клиент для загрузки списков с таймаутами и опциональным `SO_MARK`.
- `internal/log/` — собственный пакетный логгер с уровнями, буферизацией и опциональными syslog/error-file; события соединений/discovery, подписки и кольцевой буфер. CLI инициализирует вывод в stderr; отдельного HTTP API статистики/логов нет.
- `internal/leaktest/` — обертка `goleak`; `*_test.go` рядом с кодом — протоколы, маршруты, таймауты, конкурентность, утечки. Live-пробы включаются явно через переменные окружения `B4_*`, обычный прогон их пропускает.
- `config.example.json`, `mtproto-proxy.service.example`, `scripts/` — образец конфигурации, systemd unit, установка из исходников/готового бинарника. `dist/` — сборочные/дистрибутивные артефакты, не слой исходного кода.

## 3. Core Architecture Principles

- Модульный монолит / standalone network daemon с группировкой по технической ответственности; внутри `mtproto` — протокольный pipeline и стратегии upstream-транспорта. Формальных слоев Clean Architecture, MVC, DDD или микросервисов нет.
- Зависимости: `cmd → config + log + mtproto`; `mtproto → config + log`. Пакеты `internal` закрыты для внешнего импорта правилами Go.
- `Server` владеет listener, контекстом, пулами, активными сессиями и статистикой; транспортные обертки используют `net.Conn` / `io.ReadWriteCloser`, общий `relayConns` обслуживает двунаправленный обмен.
- Конфигурация, refresh адресов и здоровье маршрутов реализованы рядом с handshake/dial/relay в одном пакете; часть состояния пакетная (`cfBalancerInst`, кеши, `sharedWSPool`). Не предполагать полную изоляцию нескольких `Server` в одном процессе.

## 4. Data Flow & State

- Запуск: `копия DefaultConfig → JSON(-config) поверх defaults → CLI overrides → StartUpstreamRefresh(ctx) + NewServer(&cfg).Start()`. `-secret` заменяет список секретов; при его отсутствии `-secret-host` также заменяет список. CLI принудительно включает MTProto. Defaults: `0.0.0.0:3128`, `fake_sni=storage.googleapis.com`, `upstream_mode=auto`; отсутствующий/пустой JSON не считается ошибкой.
- TCP: `Telegram → TCP accept/лимит → fake-TLS (HMAC + timestamp, выбор секрета) → учет сети/проверка отзыва → obfuscated2 (DC + protocol tag) → planTransports/dialObfuscatedDC → Telegram DC → двунаправленный relay`. Некорректный fake-TLS с `FakeTLSVerifyError` может пересылаться домену-прикрытию с восстановлением прочитанного ClientHello.
- Upstream: `tcp` — прямой TCP/`dc_relay`; `ws` — WS-маршруты без TCP fallback; `auto` — native WS (DC2/DC4), custom/CF-proxy домены, Worker, затем TCP; заданный `dc_relay` в `auto` выдвигает TCP вперед. Это приоритет планов, не строго последовательные попытки: выбор учитывает гонки, cooldown, пулы и оставшийся бюджет. Пустой `upstream_mode` в планировщике означает `tcp`, хотя default конфига — `auto`. IPv6 зависит также от реального маршрута хоста.
- WEB: `GET /?bridge=<capability> → одноразовый ticket → WS /api/v1/ws (subprotocol tproxy-v1.<ticket>) → webSession → webStream → serveClient/obfuscated2 → общий upstream/relay`. Capability связана с hostname/секретом; fake-TLS на этом пути отсутствует. Потоки мультиплексируются с окнами/кредитами. Listener включается через `web_proxy.enabled` и отдельный `port`; TLS — при заданных PEM, иначе HTTP за внешним TLS-терминатором; посторонние запросы получают страницу-заглушку. HTML встроен в Go, заглушку можно переопределить `webproxy_page.html` рядом с конфигом.
- Постоянное состояние — JSON-конфиг/секреты. `SaveToFile` напрямую перезаписывает файл через `os.WriteFile` (не атомарно); `0600` задается при создании, права существующего файла не меняются. Секрет автоматически создается только при пустом списке; без `-config` остается в памяти. Ошибка сохранения автосекрета логируется, запуск продолжается.
- Фоновый refresh: DC-список загружается при включении/смене источника и повторяется после ошибок; после успеха часового обновления DC нет. CF-список при включенном fallback обновляется каждый час. Ошибки обоих источников повторяются с backoff `30 с → … → 1 ч`; текущие адреса/пул сохраняются.
- Runtime: atomic pointers на конфиг/секреты/пулы, atomic-счетчики, mutex-защищенные карты сессий/сетей/статистики; пакетные кеши адресов, здоровья маршрутов и fail-open предпочтений. Базы сессий/персистентной статистики нет.
- `Server.UpdateConfig` поддерживает замену конфигурации, live-reload секретов с закрытием отозванных соединений и выборочный restart/reconcile listeners. В CLI нет file watcher/SIGHUP reload: изменения JSON требуют перезапуска процесса. SIGINT/SIGTERM отменяют refresh и вызывают `Server.Stop()`; тот останавливает listeners/пулы, но не ожидает завершения всех TCP-relay и не закрывает их явно. Не считать это полным graceful drain.

## 5. Critical Invariants & Code Style

- Сохранять wire compatibility fake-TLS/obfuscated2: секрет `hex(0xee || key[16] || hostname)`, handshake obfuscated2 — 64 байта; поддерживаются abridged/intermediate/padded. Секрет определяется HMAC, не именем; до совпадения fake-TLS не отвечает ServerHello.
- Не генерировать новый доступ при явно отключенных секретах; если настроенные включенные секреты все невалидны — ошибка. В логах соединений использовать `Secret.Label()`, не ключ/hex; CLI намеренно печатает секреты и ссылки.
- `max_networks`: IPv4-адрес или IPv6 `/64` — одна сеть; `0` без ограничения; превышение отклоняет новое подключение без вытеснения существующих. Общий default limit — 2048 TCP-подключления.
- Для WEB carrier действуют отдельные ограничения: до 256 carrier на процесс, ticket TTL 2 минуты. `X-Forwarded-For` принимается только от loopback/private непосредственного пира; сохранять эту проверку, иначе подмена IP обходит учет сетей.
- Один общий dial budget **4.5 с** на выбор upstream; connect/TLS/upgrade делят deadline. Сохранять ограничение конкурентных попыток, проверку живости pooled connections и закрытие проигравших/неиспользованных соединений.
- MTProto transport errors семантически различны: `-404` передается клиенту; `-444` означает неверный DC — маршрут понижается, соединение обрывается. Не превращать любые upstream-ошибки в blacklist всех fallback-маршрутов.
- Исходящий WS использует `InsecureSkipVerify`; при fronting (`SNI != Host`) дополнительно проверяет наличие имени `telegram.org` в сертификате. Это текущая протокольная особенность, не полноценная проверка цепочки доверия.
- Не мутировать опубликованный runtime-конфиг конкурентно: обновления проводить через новый снимок/`UpdateConfig`; общие карты защищать mutex, счетчики/указатели — atomics. Горутины, timers, listeners и соединения должны иметь явный путь завершения; сохранять проверки `goleak`.
- Таймауты брать из кода: `idle_timeout_sec=0` означает **300 с**, `tcp_user_timeout_sec=0` — **120 с**; отрицательные значения отключают соответствующий таймаут (таблица README неточна).
- Go-style: `gofmt`, MixedCaps, ранние `if err != nil`, ошибки возвращать с контекстом (`%w` для цепочки), `errors.Is/As` для классификации, `defer` для освобождения ресурсов. Использовать существующий `internal/log`; новые инфраструктурные слои/зависимости вводить только под конкретную задачу.
