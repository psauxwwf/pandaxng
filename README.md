# PandaXNG

Web metasearch powered by **Lightpanda PandaScript**: DuckDuckGo, Brave, Yahoo, and Bing.
No API keys, accounts,
paid APIs, Puppeteer, or Playwright. This is a standalone project, not a SearXNG plugin.

## Installation and build

Go CLI built with **Cobra + Fang**. No built-in LLM or configuration
files: query → Lightpanda → JSON/Markdown/HTML/text semantic tree.
Includes an MCP server built with `mcp-go`.
JS parsers are embedded in the binary using `go:embed`.

Requirements:

- Go 1.27+ to build; Go is not required to run the compiled binary.
- Lightpanda with the `run` and `fetch` commands and PandaScript `Page` (tested with 1.0.1).
  Installation: <https://lightpanda.io/docs/run-locally/installation/one-liner>.

Build: `task build` → `bin/pandaxng` (Linux amd64,
`CGO_ENABLED=0`, static build). Requires Task and binutils (`strip`, `objcopy`).

Release: `task release` rebuilds the binary, packages it as
`bin/pandaxng_linux_amd64.tar.gz`, and publishes a GitHub Release tagged
`vMM.DD.HH` (for example, `v10.19.16`). Requires `tar` and an authenticated GitHub CLI
(`gh auth login`). Publishing again with the same tag fails.

Alternatively, build directly with Go:

```sh
go build -o bin/pandaxng ./cmd/pandaxng
```

## Usage

Three commands with no aliases: `search` to search (JSON), `fetch` to read a page
(Markdown by default, also HTML or a text semantic tree),
and `mcp` to start the MCP server.

`search`, `fetch`, and `mcp` support `--wait-until`. Defaults
are the same for the CLI and MCP tools:

| Operation                 | Wait mode          | Timeout               |
| ------------------------- | ------------------ | --------------------- |
| `search`                  | `domcontentloaded` | 20 seconds per engine |
| `fetch` / `fetch_content` | `done`             | 40 seconds            |

| Mode                | When to read the page                                      |
| ------------------- | ---------------------------------------------------------- |
| `domcontentloaded`  | After the DOM is built, without waiting for all resources   |
| `load`              | After the page load event                                  |
| `networkalmostidle` | When very few network requests remain                      |
| `networkidle`       | When network activity has subsided                         |
| `done`              | After all pending scripts and network requests have finished |

Waiting is bounded by `--timeout`. Continuous background
activity can cause `done` to time out even when results are already available.
For example, you can use `--wait-until networkalmostidle` for Yahoo.
Earlier wait modes may return a page before JavaScript has populated it;
waiting longer does not fix network errors or unavailable IPv6 connectivity.

### Search: `search`

```sh
./bin/pandaxng search 'linux namespaces'
./bin/pandaxng search 'what are containers' --engines duckduckgo,yahoo --language ru-RU
./bin/pandaxng search 'lightpanda browser' --engines brave --limit 5 --timeout 25s
./bin/pandaxng search 'lightpanda browser' --engines bing --limit 5
./bin/pandaxng search 'lightpanda browser' --engines yahoo --wait-until networkalmostidle
./bin/pandaxng search --list-engines
./bin/pandaxng --help
```

Defaults: all four engines, up to 10 results per engine,
four concurrent browsers, `domcontentloaded`, 20 seconds per engine, language `en-US`.

| Flag             | Description                                                               |
| ---------------- | ------------------------------------------------------------------------- |
| `--engines`      | Comma-separated engines: `duckduckgo,brave,yahoo,bing`                       |
| `--limit`        | Results per engine, 1–50 (default: 10)                                     |
| `--jobs`         | Concurrent browsers, 1–4 (default: 4)                                     |
| `--timeout`      | Per-engine timeout: Go duration (`20s`, `1m`), range 1s–2m                 |
| `--wait-until`   | Page wait mode from the table above (default: `domcontentloaded`)          |
| `--language`     | Browser language (default: `en-US`)                                       |
| `--lightpanda`   | Path to the executable (default: `lightpanda` from PATH)                    |
| `--list-engines` | List supported engines without searching                                  |

Output is JSON grouped by the selected engine names, identical for the CLI
and MCP. There is no combined result list or merging of results across engines:

```json
{
  "duckduckgo": {
    "status": "ok",
    "results": [{"url": "https://example.com/", "title": "Example", "content": "Description from DuckDuckGo"}],
    "url": "https://html.duckduckgo.com/html/?q=example",
    "title": "example at DuckDuckGo",
    "elapsed": 1.2
  },
  "bing": {
    "status": "ok",
    "results": [{"url": "https://example.com/", "title": "Example website", "content": "Description from Bing"}],
    "url": "https://www.bing.com/search?count=10&q=example",
    "title": "example - Search",
    "elapsed": 0.8
  }
}
```

Each engine returns its own ordered `results` array with the fields
`url`, `title`, and `content`. The same URL may appear under multiple
engines: titles and descriptions are kept separately. Each group includes `status`,
execution time in `elapsed`, the search page URL and title (when available),
and `error` diagnostics on failure. Empty results or errors include `results: []`.
Possible statuses: `ok`, `empty`, `blocked`, `parse_error`, `timeout`, `error`.

If at least one engine returns `ok` or `empty`, the exit code is `0`;
otherwise, it is `1`. Argument errors also exit with code `1`.
Partial failures remain in the corresponding engine's group.
`empty` means a recognized page with no
results, not just any page the parser failed to handle.
`Ctrl+C` cancels running processes.

DuckDuckGo, Yahoo, and Bing redirect links are resolved to destination
URLs; invalid URLs and duplicates within the same engine are discarded.
The four engines use
no more than four browsers at once; with a lower `--jobs` value, the rest wait for a free slot.
For Bing, `--limit` (`max_results` in MCP) is also passed to the search engine
as the `count` parameter. This is the requested result count: Bing may return fewer,
and values above 10 do not guarantee more results without pagination.

### Read a page: `fetch`

```sh
./bin/pandaxng fetch 'https://ident.me'
./bin/pandaxng fetch 'https://example.com' --timeout 30s --language ru-RU
./bin/pandaxng fetch 'https://example.com' --wait-until load
./bin/pandaxng fetch 'https://example.com/article' > article.md
./bin/pandaxng fetch 'https://example.com' --format html > page.html
./bin/pandaxng fetch 'https://example.com' --format semantic_tree_text
```

The `--format` flag selects the output format:

| Format               | Content                                                               |
| -------------------- | --------------------------------------------------------------------- |
| `md` (default)       | Markdown; `markdown` is a CLI alias                                    |
| `html`               | HTML DOM after JavaScript execution, not the original HTTP response    |
| `semantic_tree_text` | A compact text semantic tree with roles and UI elements                |

The semantic tree is useful for describing interfaces, but may omit
link URLs and preserve code formatting less accurately. For reading pages,
`md` is usually more convenient.

Runs `lightpanda fetch --dump <format> --log-format pretty --log-level info`
(`md` is converted to `markdown`)
with the same User-Agent and TLS flag as search. The default wait mode is `done`,
with a 40-second timeout. The page is loaded with JavaScript
execution, and its full content is printed in the selected format (without a JSON wrapper).
Lightpanda logs are kept separate
from the content; on failure, diagnostics go to stderr and the exit code is `1`.
HTTP 4xx/5xx responses are treated as errors. Supports `--format`, `--timeout`, `--wait-until`, `--language`,
and `--lightpanda`; `Ctrl+C` stops the process.

The CLI `fetch` command and the MCP `fetch_content` tool share the same implementation.
Caching and pagination are not yet supported.
Only HTTP(S) URLs without credentials are allowed, but private network access
is not blocked in the local CLI command: only pass trusted URLs.
MCP mode also allows this access by default; use
`--disable-private-urls` to block it. TLS hostname verification is disabled
(see “How it works”).

### MCP server: `mcp`

Uses [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go).

```sh
# stdio is the default; stdout contains only JSON-RPC
./bin/pandaxng mcp
./bin/pandaxng mcp --transport stdio
./bin/pandaxng mcp --wait-until networkalmostidle

# Streamable HTTP, endpoint http://127.0.0.1:8080/mcp
./bin/pandaxng mcp --transport http
./bin/pandaxng mcp --transport http --listen 127.0.0.1:9090 --timeout 30s
```

Both transports expose the same tools:

| Tool            | Parameters                                                                                                                                                                                                   | Result                                                                                                   |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------- |
| `search`        | `query` (required), `max_results` (1–50, default: 10 per engine), `engines` (array of 1–4 entries: `duckduckgo`, `brave`, `yahoo`, `bing`; defaults to all four), `language`, `wait_until`, `timeout` | JSON keyed by engine name: each engine's own `results`, `status`, and diagnostics; also in `structuredContent` |
| `fetch_content` | `url` (required), `format` (optional: `md`, `html`, `semantic_tree_text`; default: `md`), `language`, `wait_until`, `timeout`                                                                                    | Page content in the selected format after the selected load event                                         |

The server flags `--lightpanda`, `--timeout`, `--language`, and `--wait-until` set
shared options. Without these flags, the tools use different defaults:
`search` uses `domcontentloaded` / 20 seconds; `fetch_content` uses `done` / 40 seconds.
Explicit `mcp --timeout` and `mcp --wait-until` flags override the corresponding setting
for both tools. The `wait_until` and `timeout` arguments are **optional**:
omit them to use the server settings.
`wait_until` overrides the mode for a single call; the same five values are accepted.
`timeout` is an integer number of seconds from 1 to 120 (per engine for search),
overriding the browser timeout and the overall deadline for the call, including slot wait time.
Precedence: MCP request argument → server flag → tool default.

Example MCP tool arguments:

The `format` parameter of `fetch_content` is **optional**: omitting it
returns Markdown (`md`), just like the CLI. For HTML, pass:

```json
{ "url": "https://example.com", "format": "html" }
```

For a text semantic tree:

```json
{ "url": "https://example.com", "format": "semantic_tree_text" }
```

```json
{ "query": "lightpanda browser" }
```

```json
{ "query": "lightpanda browser", "engines": ["bing"] }
```

```json
{ "url": "https://ident.me", "wait_until": "done", "timeout": 60 }
```

Tool errors are returned as MCP `isError`; when search partially succeeds,
failures from the other engines remain in the report. At most
3 tool calls run concurrently, with each search launching up to 3 browsers. Slot wait time is also
bounded by the timeout. `Ctrl+C` stops the server and browsers; closing stdin
shuts down the stdio server.

Example stdio configuration for an MCP client (replace the path with an absolute path):

```json
{
  "mcpServers": {
    "pandaxng": {
      "command": "/path/to/pandaxng/bin/pandaxng",
      "args": ["mcp"]
    }
  }
}
```

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "PANDAXNG": {
        "type": "local",
        "command": ["pandaxng", "mcp"]
      }
    }
  }
}
```

**Security:** MCP allows network requests to private
addresses by default — use it only with trusted clients (SSRF risk).
To block this access, run `mcp --disable-private-urls`: Lightpanda receives
`--block-private-networks`, which also applies to redirects and page
resources. HTTP listens on localhost, validates
Host through the SDK, and rejects foreign Origin headers. There is no built-in authentication or TLS
for incoming HTTP: do not expose the port to the internet without a secured reverse
proxy. If the proxy connects over loopback, set the upstream Host to localhost.
The TLS flag for outgoing requests remains insecure, just as in the CLI.

## How it works

The Go binary builds the query and passes an embedded JS script to
`lightpanda run -` via stdin. `scripts/common.js` opens the page with
`Page.goto()`, runs the DOM parser through `Page.evaluate()`, and returns an object.
Each engine has its own selectors in `scripts/engines/`. Results
are extracted from public web pages, not private APIs. To adapt to changes
in a search engine's markup, edit its JS file.
Rebuild the binary after changing embedded scripts.

Structure:

- `cmd/pandaxng/` — `search`, `fetch`, and `mcp` commands and Fang styling;
- `internal/pandaxng/` — Lightpanda execution, URL validation, and per-engine results;
- `internal/mcpserver/` — MCP tools and stdio/HTTP transports;
- `scripts/embed.go` — embedded JS parsers;
- `scripts/common.js`, `scripts/engines/` — shared search logic and engine parsers.

Every invocation uses these flags:

```text
--user-agent "AppleWebKit/537.36 (X11; Linux x86_64) (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"
--insecure-disable-tls-host-verification
```

**Warning:** the second flag disables TLS certificate hostname verification.
It is enabled at the user's request and reduces connection security.

## Limitations

- Only the first page of regular web search is supported. Images, news, video,
  and pagination are outside the scope of this project.
- No public search engine guarantees CAPTCHA-free access from every IP.
  Lightpanda does not guarantee bypassing anti-bot protection.
- CAPTCHAs are not solved automatically; there are no retries.
  No accounts or user tokens are used. Sites may set
  ordinary temporary cookies within a single browser process.
- Site markup changes; `parse_error` explicitly indicates mismatched
  selectors, an incomplete page, or an unknown blocking mechanism.
- Sessions are not persisted between requests. Requests are direct, with no proxy configuration.
- `--language` sets the browser language/Accept-Language, but does not guarantee a results region.

## Tests

```sh
go test -race ./...
go vet ./...
```

Tests cover the CLI, URLs, process execution, error handling, per-engine grouping,
and MCP over stdio and HTTP without querying external search engines.
When Lightpanda is available, integration tests start a local HTTP server
and verify page fetching with JavaScript execution, HTTP error handling,
and private network blocking. Local Bing HTML fixtures verify
result and snippet extraction, ad filtering, CAPTCHA detection, and empty results.
This test group is skipped when Lightpanda is unavailable.
For a live network smoke test, use the command above and check
each group's status (`duckduckgo.status`, `bing.status`, etc.), not just the overall exit code.

### Network test results

From the current IP, using the query `lightpanda browser`:

| Engine                   | Observation                                                                  |
| ------------------------ | ---------------------------------------------------------------------------- |
| DuckDuckGo, Brave, Yahoo | Relevant results received                                                    |
| Bing (2026-11-02)        | CLI and MCP returned `ok` without CAPTCHA; links resolved to direct URLs      |

This table reflects observations from one environment, not a guarantee of availability from another IP.
`ok` means
successful result extraction, not an automatic assessment of relevance.
