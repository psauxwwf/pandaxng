# PandaXNG

Веб-метапоиск через **Lightpanda PandaScript**: DuckDuckGo, Brave и Yahoo.
Без API-ключей, аккаунтов,
платных API, Puppeteer и Playwright. Это самостоятельный проект, не плагин SearXNG.

## Минимальный Go-пример

CLI на **Cobra + Fang**, по образцу `pmgbot`. Без LLM, сервера и конфигурационных
файлов: запрос → Lightpanda → JSON. JS-парсеры встроены в бинарник через
`go:embed`, Python для Go-версии не нужен.

Требуются Go 1.27+ для сборки и установленный Lightpanda для поиска.

Сборка по образцу `pmgbot`: `task build` → `bin/pandaxng` (Linux amd64,
`CGO_ENABLED=0`, статическая сборка). Требуются Task и binutils (`strip`, `objcopy`).

```sh
go build -o bin/pandaxng ./cmd/pandaxng
./bin/pandaxng search 'linux namespaces'
./bin/pandaxng search 'что такое контейнеры' --engines duckduckgo,yahoo --language ru-RU
./bin/pandaxng search 'lightpanda browser' --engines brave --limit 5 --timeout 25s
./bin/pandaxng search --list-engines
./bin/pandaxng --help
```

`--timeout` — Go duration (`20s`, `1m`), диапазон 1s–2m. Остальные значения
по умолчанию и формат JSON совпадают с Python-примером ниже. Ошибки завершаются
кодом `1`; если хотя бы один движок вернул `ok` или `empty`, код — `0`.
Частичные ошибки остаются в `engines`. `Ctrl+C` отменяет запущенные процессы.

Структура:

- `cmd/pandaxng/main.go` — Cobra-команда и оформление Fang;
- `internal/pandaxng/` — запуск Lightpanda, проверка URL и объединение выдачи;
- `scripts/embed.go` — встраивание существующих JS-парсеров.

```sh
go test -race ./...
go vet ./...
```

Две команды без алиасов: `search` для поиска (JSON) и `fetch` для чтения страницы
(Markdown).

### Чтение страницы: `fetch`

```sh
./bin/pandaxng fetch 'https://ident.me'
./bin/pandaxng fetch 'https://example.com' --timeout 30s --language ru-RU
./bin/pandaxng fetch 'https://example.com/article' > article.md
```

Запускает `lightpanda fetch --dump markdown --log-format pretty --log-level info`
с теми же User-Agent и TLS-флагом, что и поиск. Страница загружается с выполнением
JavaScript, выводится Markdown целиком (без JSON). Логи Lightpanda не смешиваются
с содержимым; при сбое диагностика выводится в stderr, код завершения — `1`.
HTTP 4xx/5xx считаются ошибками. Поддерживаются `--timeout`, `--language`,
`--lightpanda`; `Ctrl+C` останавливает процесс.

Это локальная CLI-команда, не MCP-инструмент. Пока без кэша и пагинации.
Разрешены только HTTP(S) URL без логина/пароля, но доступ во внутреннюю сеть
не блокируется: передавайте доверенные URL. При публикации через сервер
потребуется отдельная защита от SSRF. Проверка имени хоста TLS отключена
по указанному выше требованию пользователя.

## Прежний Python-пример

Сохранён отдельно; Go-версия его не запускает.

### Требования

- Python 3.10+ — только стандартная библиотека, `pip install` не нужен.
- Lightpanda с командой `run` и PandaScript `Page` (проверено с 1.0.1).
  Установка: <https://lightpanda.io/docs/run-locally/installation/one-liner>.

### Запуск

```sh
python3 pandaxng.py 'linux namespaces'
python3 pandaxng.py 'что такое контейнеры' --engines duckduckgo,yahoo --language ru-RU
python3 pandaxng.py 'lightpanda browser' --engines brave --limit 5 --timeout 25
python3 pandaxng.py --list-engines
```

По умолчанию: все три движка, максимум 10 результатов с каждого,
три параллельных браузера, 20 секунд на движок. Вывод — JSON:

- `results`: объединённые результаты `{url, title, content, engines}`;
- `engines`: исходные результаты и состояние каждого движка;
- состояния: `ok`, `empty`, `blocked`, `parse_error`, `timeout`, `error`.

Результаты чередуются между движками, одинаковые URL объединяются с указанием
источников. Exit code `1` означает, что ни один движок не дал успешного ответа;
`2` — ошибка аргументов. `empty` — распознанная страница без результатов, а не
любая страница, на которой не сработал парсер.

## Как работает

`pandaxng.py` собирает запрос и передаёт пользовательский JS-скрипт в
`lightpanda run -` через stdin. `scripts/common.js` открывает страницу через
`Page.goto()`, выполняет DOM-парсер через `Page.evaluate()` и возвращает объект.
У каждого движка собственные селекторы в `scripts/engines/`. Результаты
извлекаются из публичной веб-страницы, а не из закрытых API. Для изменения
разметки поисковика достаточно поправить его JS-файл.

Каждый запуск обязательно использует:

```text
--user-agent "AppleWebKit/537.36 (X11; Linux x86_64) (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"
--insecure-disable-tls-host-verification
```

**Внимание:** второй флаг отключает проверку имени хоста TLS-сертификата.
Он включён по требованию пользователя и снижает безопасность соединений.

## Ограничения

- Реализована первая страница обычного веб-поиска. Картинки, новости, видео,
  пагинация и серверный HTTP/MCP-интерфейс не входят в этот проект.
- Ни один публичный поисковик не гарантирует доступ без CAPTCHA с любого IP.
  Lightpanda не гарантирует обход антибот-защиты.
- CAPTCHA не решается автоматически; повторов нет.
  Аккаунты и пользовательские токены не используются. Сайты могут выставлять
  обычные временные cookies в рамках одного браузерного процесса.
- Разметка сайтов меняется; `parse_error` явно сообщает о несовпадении
  селекторов, неполной странице или неизвестной блокировке.
- Сессии между запросами не сохраняются. Запросы идут напрямую, без настройки прокси.
- `--language` задаёт язык браузера/Accept-Language, но не гарантирует регион выдачи.

## Тесты

```sh
python3 -m unittest discover -s tests -v
```

Тесты проверяют URL, запуск, обработку ошибок и агрегацию без внешних
запросов. При наличии Lightpanda интеграционные тесты поднимают локальный
HTTP-сервер и проверяют парсеры всех трёх движков на синтетических HTML-страницах,
включая отложенный JavaScript, обнаружение блокировок и исключение рекламы.
Без Lightpanda эта группа пропускается.
Для реального сетевого smoke-теста используйте команду запуска выше и смотрите
`engines[].status`, а не только общий exit code.

### Результат первой сетевой проверки

На текущем IP с запросом `lightpanda browser`:

| Движок | Наблюдение |
|---|---|
| DuckDuckGo, Brave, Yahoo | Получены релевантные результаты |

Эта таблица — наблюдение одного окружения, не гарантия доступности с другого IP.
`ok` означает
успешное извлечение карточек, а не автоматическую оценку их релевантности.
