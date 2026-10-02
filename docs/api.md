# DT Collector upload API, version 1

This is the contract between the uploaders (SwarmDialer, and the
hardware collection script `hw-collect`) and DT Collector. Change it in
all projects together. The report format itself is defined
by `internal/report/report.go`; this document describes it and the rules
the server enforces.

## Transport and authentication

- Base URL: `https://<dns-name>`. The certificate is a real Let's Encrypt
  certificate, so SwarmDialer verifies it normally (no self-signed
  acceptance). HTTP requests are redirected to HTTPS; the API should always
  be called with `https://`.
- Every call carries `Authorization: Bearer <API key>`.
- API (upload) keys look like `dtk_` followed by 43 characters from
  `[A-Za-z0-9]` (47 characters in total). An admin creates them in the web
  interface under **API (Upload) Keys**; the full key is shown once. A key
  can only call the two endpoints below.

Error bodies are always JSON: `{"error": "<message>"}`.

## `GET /api/v1/ping`

Checks a key. SwarmDialer calls this when the user enters the key in the
Setup Wizard, before saving it.

| Status | Body | Meaning |
|---|---|---|
| 200 | `{"status":"ok"}` | Key is valid and not revoked |
| 401 | `{"error":"missing, invalid or revoked upload key"}` | Anything else |

## `POST /api/v1/reports`

Uploads one report: the JSON document below as the request body.
`Content-Encoding: gzip` is accepted and recommended (reports compress
roughly 5 to 10 times).

| Status | Body | Meaning |
|---|---|---|
| 201 | `{"id":"<report_id>"}` | Stored |
| 200 | `{"id":"<report_id>","duplicate":true}` | A report with this `report_id` is already stored. Uploads are idempotent, so a retry after a lost response is safe. The stored copy is kept as it was. |
| 400 | `{"error":"<field>: <problem>"}` | Fails validation. The message starts with the field path, e.g. `tests[2].result.stop_reason: must be one of ...` |
| 401 | `{"error":"missing, invalid or revoked upload key"}` | Key missing, wrong or revoked |
| 413 | `{"error":"report too large","max_bytes":20971520}` | Body over 20 MB, compressed or decompressed |
| 415 | `{"error":"Content-Encoding must be gzip or absent"}` | Other content encodings |
| 500 | `{"error":"internal error"}` | Server problem; retry later |

Retry advice for SwarmDialer: retry on network errors, 5xx and 429 with
backoff; do not retry 400, 401, 413 or 415 without a change (they will fail
again). Keep the local copy either way.

## Report format, version 1

Two kinds of report share this format and endpoint:

- **Benchmark report**: one per completed run of a test script (profile
  `standard`, `smoke`, ...), with at least one entry in `tests`.
- **Hardware-only report**: profile **`{"name": "hardware", "version": 1}`**
  and **`"tests": []`**. It records a host's hardware and versions without
  tests. SwarmDialer's "Upload Hardware Info Only" sends one (with its
  VPS and PBXware sections filled); the hardware collection script sends
  one with `"vps": {}` and `"pbxware": []` and a `source` field instead of
  `swarmdialer_version` (see below). The interface lists these as
  "Hardware only" and keeps them out of comparisons.

Benchmark report example
(a complete one, with time series, is printed by `go run ./cmd/dtc-sample`):

```json
{
  "schema_version": 1,
  "report_id": "5f0c8a52-3b7e-4c1a-9d2e-6b8f0a1c2d3e",
  "created_at": "2026-09-28T12:00:00Z",
  "swarmdialer_version": "1.4.0",
  "profile": { "name": "standard", "version": 1 },
  "environment": {
    "serverware": { "version": "5.2.1", "edition": "standalone" },
    "host": {
      "cpu_model": "AMD EPYC 7443P 24-Core Processor",
      "cpu_sockets": 1, "cpu_cores": 24, "cpu_threads": 48, "cpu_max_mhz": 4000,
      "memory_bytes": 137438953472,
      "disks": [ { "model": "Micron 5300 MTFD", "size_bytes": 1920000000000, "type": "ssd" } ],
      "network": [ { "speed_mbps": 10000 } ]
    },
    "vps": {
      "pbxware_mt":  { "cpu_limit": 0, "cpu_share": 512, "mem_limit_mb": 8192, "callrec_ram_mb": 512 },
      "pbxware_cc":  { "cpu_limit": 0, "cpu_share": 512, "mem_limit_mb": 8192, "callrec_ram_mb": 512 },
      "swarmdialer": { "cpu_limit": 0, "mem_limit_mb": 4096 }
    },
    "pbxware": [
      { "role": "MT", "version": "8.2.0.0", "edition": "Multi-Tenant", "license_channels": 512 },
      { "role": "CC", "version": "8.2.0.0", "edition": "Call Centre",  "license_channels": 512 }
    ]
  },
  "tests": [
    {
      "id": "ramp_norec_low",
      "mode": "ramp",
      "call_type": "remote",
      "codec": { "caller": "ulaw", "callee": "ulaw" },
      "recording": "off",
      "recording_format": "",
      "call_duration_s": 600,
      "dial_rate_cps": 1.25,
      "started_at": "2026-09-28T08:00:00Z",
      "finished_at": "2026-09-28T08:09:50Z",
      "result": {
        "stop_reason": "target_reached",
        "max_concurrent_calls": 512,
        "calls": { "started": 512, "answered": 512, "failed": 0 },
        "setup_ms": { "avg": 42.1, "p95": 48.6, "max": 51.0 },
        "mos": { "avg": 4.38, "min": 4.03 },
        "rtp_received_ratio": 0.9991,
        "quality_degraded_at_calls": null,
        "at_target": { "host_cpu_pct": 52.7, "host_mem_pct": 35.9, "asterisk_cpu_pct": { "MT": 1272, "CC": 893 } },
        "recording": null
      },
      "timeseries": {
        "interval_s": 5,
        "start": "2026-09-28T08:00:00Z",
        "series": {
          "concurrent_calls": [0, 0, 6, 12],
          "host_cpu_pct": [3.1, 3.0, 3.9, 4.6],
          "vps_cpu_pct": { "MT": [1.0, 1.1, 1.6, 2.2], "CC": [1.0, 1.0, 1.4, 1.8], "swarmdialer": [0.5, 0.5, 0.6, 0.6] },
          "asterisk_cpu_pct": { "MT": [0.5, 0.6, 12.0, 24.1], "CC": [0.5, 0.5, 9.0, 17.5] }
        }
      }
    }
  ]
}
```

### What the server checks

The server validates structure and the fields it relies on for listing,
filtering and comparing. Anything else is stored as uploaded.

- `schema_version`: required, must be `1`.
- `report_id`: required, a UUID (stored lowercase). It is the idempotency key.
- `created_at`, and every test's `started_at` and `finished_at`: RFC 3339
  timestamps. `finished_at` must not be before `started_at`.
- `swarmdialer_version`: required for SwarmDialer, at most 64 characters.
  Other tools send `"source": {"name": "hw-collect", "version": "1.0.0"}`
  instead (name: same character rules as `profile.name`; version: 1 to 64
  characters); `swarmdialer_version` may then be omitted. With neither,
  the report is rejected.
- `profile.name`: 1 to 64 characters from `[A-Za-z0-9_.-]`, starting with a
  letter or digit. `profile.version`: an integer, 1 or greater. Reports are
  compared only when both match.
- `environment.serverware.version`: required. `edition`, when present:
  `standalone`, `mirror` or `cluster`.
- `environment.host`: numeric fields must not be negative. All fields are
  optional (report 0 or omit what SwarmDialer can't read).
- `environment.pbxware[].role`: required for each entry.
- `tests`: 1 to 50 entries with unique `id`s (same character rules as
  `profile.name`). For profile `hardware` it must be `[]`, and
  `environment.host.cpu_model` is required. An empty `tests` list with any
  other profile is rejected.
- `environment.vps` may be `{}` and `environment.pbxware` may be `[]`.
- `environment.host` may also carry `system_vendor`, `system_model`,
  `storage_controllers[]` (`vendor`, `product`, `count`), `nics[]`
  (`vendor`, `product`, `driver`, `count`, `speed_mbps`), `bonds[]`
  (`ports`) and `motherboard` (`vendor`, `model`, `version`,
  `bios_vendor`, `bios_version`, `bios_date`). Counts and speeds must not
  be negative. The interface shows them on the report page; system vendor
  and model are also searchable.
- `tests[].mode`: `ramp` or `rolling`. `tests[].recording`: `off`, `mono`
  or `stereo`.
- `tests[].result.stop_reason`: `target_reached`, `target_not_reached`, `host_cpu_100`,
  `host_ram_100`, `vps_cpu_limit`, `vps_ram_limit`,
  `swarmdialer_overloaded` or `error`.
- `result.recording.mp3_conversion_delay_s.trend`, when present: `stable`
  or `growing`. `result.recording.ramdisk_full_estimated_at`, when not
  null: an RFC 3339 timestamp.
- Integer fields (`cpu_sockets`, `cpu_cores`, `cpu_threads`,
  `max_concurrent_calls`, `calls.*`, `license_channels`,
  `quality_degraded_at_calls`, `ramdisk_full_estimated_at_calls`) must be
  JSON integers; a type mismatch is a 400 naming the field.
- `timeseries`: when `series` is non-empty, `interval_s` must be greater
  than 0 and `start` an RFC 3339 timestamp. Each series is either an array
  of numbers or an object mapping a group name (`MT`, `CC`,
  `swarmdialer`) to such an array. `null` marks a missing sample. At most
  200,000 points per array.
- Unknown fields are accepted and kept, so SwarmDialer can add optional
  fields within version 1. A breaking change needs `schema_version: 2`.

### Diagnostics fields (SwarmDialer 1.6.0 and later)

SwarmDialer 1.6.0 adds optional fields to each test, still within format
version 1: `result.stop_detail`, `result.quality_degraded_reason`,
`result.mos.n`, `result.at_stop`, `result.failures[]`, `result.pbxware`,
`result.tool`, `result.events[]`,
`result.recording.mp3_conversion_delay_s_by_instance`,
`result.recording.missing_recordings` and
`timeseries.series.pbxware_active_calls` (`{"MT": [...], "CC": [...]}`).
Their full description is in `~/claude/SwarmDialer_report_diagnostics.md`.
The server stores them as uploaded and checks only that each
`failures[]` entry has a cause and a non-negative count (at most 100
groups and 1000 events per test). `stop_reason` may also be `cancelled`.

DT Collector treats a benchmark report as a diagnostics report when it
comes from SwarmDialer 1.6.0 or later or carries any of these fields; the
interface marks it "Diagnostics" and shows the extra sections. Older
reports are shown as before.

### Meaning of the time series

Sample `i` of every series is at `start + i * interval_s`. The web
interface plots them against elapsed time since `start`, so every series of
a test should share the same `start` and `interval_s`.

| Series | Unit (as the interface labels it) |
|---|---|
| `concurrent_calls` | calls |
| `host_cpu_pct`, `host_mem_pct`, `host_iowait_pct` | percent of the whole host (0 to 100) |
| `host_net_rx_bps`, `host_net_tx_bps` | **bits** per second |
| `host_disk_read_bps`, `host_disk_write_bps` | **bytes** per second |
| `vps_cpu_pct.<group>` | percent (as SERVERware reports it for the VPS) |
| `vps_mem_bytes.<group>` | bytes |
| `asterisk_cpu_pct.<group>` | percent of one core (can exceed 100) |
| `setup_ms_p95` | milliseconds, p95 of calls set up in the interval |
| `failed_calls` | failed calls in the interval |

The units of the network series (bits) and disk series (bytes) are the
server's assumption; confirm them when building SwarmDialer's side.

### Privacy rule

Reports never contain secrets, internal IP addresses or host names: no API
keys, passwords, VPS descriptions, IPs, VPS or host names. Only hardware
details, versions and results. SwarmDialer builds reports from an explicit
allowlist of fields. The server doesn't try to detect such data (version
strings look like IP addresses), so the allowlist on SwarmDialer's side is
what enforces this.

## Example calls

```bash
KEY=dtk_...
curl -sS https://dt.example.com/api/v1/ping -H "Authorization: Bearer $KEY"

gzip -c report.json | curl -sS https://dt.example.com/api/v1/reports \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -H "Content-Encoding: gzip" --data-binary @-
```

## Hardware-only report from the hardware collection script

```json
{
  "schema_version": 1,
  "report_id": "0b0c8a52-3b7e-4c1a-9d2e-6b8f0a1c2d3e",
  "created_at": "2026-09-29T10:00:00Z",
  "source": { "name": "hw-collect", "version": "1.0.0" },
  "profile": { "name": "hardware", "version": 1 },
  "environment": {
    "serverware": { "version": "5.2.1", "edition": "mirror" },
    "host": {
      "cpu_model": "Intel(R) Xeon(R) Silver 4208 CPU @ 2.10GHz",
      "cpu_sockets": 2, "cpu_cores": 16, "cpu_threads": 32,
      "cpu_max_mhz": 3200, "memory_bytes": 201326592000,
      "system_vendor": "Supermicro", "system_model": "SYS-6029BT-DNC0R",
      "disks": [ { "model": "SAMSUNG MZ7LH960HAJR-00005", "size_bytes": 0, "type": "ssd" } ],
      "network": [ { "speed_mbps": 10000 } ],
      "storage_controllers": [ { "vendor": "Broadcom / LSI", "product": "SAS3008 PCI-Express Fusion-MPT SAS-3", "count": 1 } ],
      "nics": [ { "vendor": "Intel Corporation", "product": "Ethernet Controller X550", "driver": "ixgbe", "count": 2, "speed_mbps": 10000 } ],
      "bonds": [ { "ports": 2 } ],
      "motherboard": { "vendor": "Supermicro", "model": "X11DPT-B", "version": "1.02",
                       "bios_vendor": "American Megatrends Inc.", "bios_version": "3.5", "bios_date": "05/15/2021" }
    },
    "vps": {},
    "pbxware": []
  },
  "tests": []
}
```

Same endpoint, API key, gzip and idempotency by `report_id` as benchmark
reports. `go run ./cmd/dtc-sample -script` prints one (`-hardware` prints
SwarmDialer's variant).
