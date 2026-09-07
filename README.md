# CPA Codex Auto Ping

A minimal CLIProxyAPI plugin that sends a tiny real Codex request to every enabled/available Codex OAuth account at configured daily times.

- Plugin ID: `codex-auto-ping`.
- Model is fixed to `gpt-5.6-luna`.
- Schedule is configured through CPA plugin config, not environment variables.
- Default schedule: `06:00`, `11:00`, `16:00`, `21:00`.
- Uses `host.auth.list`, `host.auth.get`, and `host.http.do` only.
- Does not change scheduler priority or write auth files.
- Does not ping immediately when CPA starts; it waits for the next configured time.
- Exposes a CLIProxyAPI status page and Management API for diagnostics/manual runs.

## CPA configuration

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    codex-auto-ping:
      enabled: true
      timezone: Asia/Shanghai
      times:
        - "06:00"
        - "11:00"
        - "16:00"
        - "21:00"
```

You can customize the schedule directly in CPA config:

```yaml
plugins:
  configs:
    codex-auto-ping:
      enabled: true
      timezone: Asia/Shanghai
      times:
        - "07:30"
        - "12:15"
        - "18:00"
```

Inline form is also accepted:

```yaml
plugins:
  configs:
    codex-auto-ping:
      enabled: true
      timezone: Asia/Shanghai
      times: ["06:00", "11:00", "16:00", "21:00"]
```

`timezone` must be an IANA timezone name. `times` must contain one or more `HH:MM` values.

The model is intentionally not configurable and is fixed to:

```text
gpt-5.6-luna
```

## Status page and Management API

The plugin registers a browser-navigable resource with CLIProxyAPI. After the plugin is registered, the CPA management UI can expose a `Codex Auto Ping` menu entry backed by:

```text
GET /v0/resource/plugins/codex-auto-ping/status
```

The status page shows:

- enabled state
- plugin version and model
- timezone and configured schedule
- next run time
- whether a run is currently in progress
- last run time, attempted/succeeded/failed counts, and the first error if any
- a `Run Now` action

Authenticated Management API routes are also registered:

```text
GET  /v0/management/plugins/codex-auto-ping/status
POST /v0/management/plugins/codex-auto-ping/run
```

The `POST .../run` endpoint starts a run asynchronously and returns HTTP 202. If another run is already in progress it returns HTTP 409.

## Build

Linux:

```bash
CGO_ENABLED=1 go build -buildmode=c-shared -o codex-auto-ping.so .
```

macOS:

```bash
CGO_ENABLED=1 go build -buildmode=c-shared -o codex-auto-ping.dylib .
```

Then copy the resulting shared library into CPA's configured plugin directory.

## CPA Plugin Store source

Add this custom source to CPA:

```text
https://raw.githubusercontent.com/jiz4oh/cpa-plugin-codex-auto-ping/main/registry.json
```

Release packages use CPA's standard naming convention, for example:

```text
codex-auto-ping_0.2.3_linux_amd64.zip
└── codex-auto-ping.so
```

## Behavior

At each configured time the plugin:

1. Lists CPA auth records.
2. Selects enabled and available Codex accounts.
3. Reads each selected auth record.
4. Sends a minimal request to `https://chatgpt.com/backend-api/codex/responses` using `gpt-5.6-luna` and prompt `ping`.
5. Logs per-account success/failure and a final summary.

The request uses `store: false` and `stream: true`. It is still a real model request and therefore can consume a small amount of quota.

## Repository

https://github.com/jiz4oh/cpa-plugin-codex-auto-ping
