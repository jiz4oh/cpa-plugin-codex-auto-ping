# CPA Codex Auto Ping

A minimal CLIProxyAPI plugin that sends a tiny real Codex request to every enabled/available Codex OAuth account at configured daily times.

- Model is fixed to `gpt-5.6-luna`.
- Schedule is configured through CPA plugin config, not environment variables.
- Default schedule: `06:00`, `11:00`, `16:00`, `21:00`.
- Uses `host.auth.list`, `host.auth.get`, and `host.http.do` only.
- Does not change scheduler priority or write auth files.
- Does not ping immediately when CPA starts; it waits for the next configured time.

## CPA configuration

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    auto-ping:
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
    auto-ping:
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
    auto-ping:
      enabled: true
      timezone: Asia/Shanghai
      times: ["06:00", "11:00", "16:00", "21:00"]
```

`timezone` must be an IANA timezone name. `times` must contain one or more `HH:MM` values.

The model is intentionally not configurable and is fixed to:

```text
gpt-5.6-luna
```

## Build

Linux:

```bash
CGO_ENABLED=1 go build -buildmode=c-shared -o auto-ping.so .
```

macOS:

```bash
CGO_ENABLED=1 go build -buildmode=c-shared -o auto-ping.dylib .
```

Then copy the resulting shared library into CPA's configured plugin directory.

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
