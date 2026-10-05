# Agent

Collects host metrics (CPU, memory, disk, network) and sends them to Middle
Monitor. It can also scrape endpoints in the Prometheus exposition format,
discover its targets through Nomad, DNS SRV or http_sd, and expose what it holds
to an existing Prometheus.

## Install

```bash
curl -fsSL https://YOUR_API_URL/api/v1/agents/download/install | bash
```

The script detects the OS and architecture, downloads the matching binary,
verifies its sha256, writes the configuration and installs a service (systemd
on Linux, launchd on macOS).

To update an installed agent:

```bash
curl -fsSL https://YOUR_API_URL/api/v1/agents/download/update | bash
```

The new binary is downloaded and checked before the service is stopped, so a
failed download leaves the running agent alone. The previous binary is kept as
`/usr/local/bin/middle-monitor-agent-<os>-<arch>.backup`.

### With a configuration management tool

`curl | bash` is fine to try the agent on one machine, not for a fleet. The
binary, its version and its checksum can be fetched on their own:

```bash
# Served version, checksums and versioned URLs for every platform
curl -fsSL https://YOUR_API_URL/api/v1/agents/latest
# {"version":"1.4.2","sha256":{"linux/amd64":"..."},"url":{"linux/amd64":".../download/1.4.2/linux/amd64"}}

# Checksum only, in shasum format (usable as Ansible get_url checksum:)
curl -fsSL https://YOUR_API_URL/api/v1/agents/download/linux/amd64/sha256

# Download and verify without installing anything
curl -fsSL https://YOUR_API_URL/api/v1/agents/download/install -o install.sh
bash install.sh --download-only
```

The download URL accepts a pinned version (`/api/v1/agents/download/1.4.2/linux/amd64`);
a pinned version that is no longer served answers `404` instead of silently
delivering another binary. Responses carry an `ETag` (the sha256),
`Last-Modified`, `X-Agent-Version` and `X-Agent-SHA256`, so a deployment replayed
on an up-to-date fleet gets `304`s instead of 14 MB per host.

## Build from source

Requires Go 1.21+.

```bash
go build -o middle-monitor-agent .
./build.sh 1.4.2   # every served platform into dist/, with checksums
```

## Configuration

Start from [config.yaml.example](config.yaml.example), which documents every option:

```yaml
api:
  url: 'https://api.middlemonitor.io'

host:
  name: 'my-server' # defaults to the hostname
  service: 'my-service'

metrics:
  cpu: true
  ram: true
  disk: true
  network: true

interval: 60 # seconds between collections
```

```bash
./middle-monitor-agent                                  # /etc/middle-monitor/config.yaml
./middle-monitor-agent --config /path/to/config.yaml    # or MIDDLE_MONITOR_CONFIG=...
./middle-monitor-agent --version
./middle-monitor-agent --config-check --config /etc/middle-monitor/config.yaml
```

`--config-check` touches neither the network nor the exporter and exits non-zero
on a broken file, so a deployment tool can refuse to restart the agent on a
configuration it just broke (Ansible `template` with `validate:`).

`SIGHUP` reloads the configuration (targets, filters, credentials, discovery)
without dropping running scrape cycles. An invalid file is rejected and the
agent keeps the previous one.

```bash
systemctl reload middle-monitor-agent   # needs ExecReload=/bin/kill -HUP $MAINPID
```

## Prometheus scraping

Besides its own metrics, the agent can pull any endpoint serving the
Prometheus format (node_exporter, an instrumented app, a sidecar). Series keep
their labels and become queryable in the metrics explorer. Disabled by default:
an upgraded agent behaves exactly as before until it is turned on.

```yaml
scrape:
  enabled: true
  interval: 15 # default seconds between scrapes
  timeout: 10 # default seconds before a target is given up on
  targets:
    - name: node # added to every series as scrape_target
      url: 'http://localhost:9100/metrics'
      labels:
        env: prod
    - name: my-app
      url: 'http://localhost:8080/metrics'
      interval: 30
```

Each target runs its own loop, so a slow endpoint never delays the others.
Target labels win over a label of the same name from the endpoint. Counters,
gauges and untyped metrics are stored raw and rates are computed at query time.
A malformed line is skipped rather than failing the scrape, and `NaN` is dropped
rather than stored as zero.

### Filtering series

One node_exporter exposes over a thousand series per host when thirty usually
do. Filtering happens on the agent: what is dropped never crosses the network.

```yaml
targets:
  - name: node
    url: 'http://localhost:9100/metrics'
    keep_metrics: # allow list: when set, everything else is dropped
      - '^node_(cpu|memory|disk_io|vmstat)_.*'
      - '^node_(boot_time_seconds|load(1|5|15))$'
    drop_metrics: # applied to what keep_metrics let through
      - '^(go_|prometheus_|promhttp_).*'
    keep_if_labels: # keep a metric only for one label value
      - metric: '^node_filesystem_.*'
        label: fstype
        matches: '^ext4$'
    drop_labels: [id, uuid] # remove a label without losing the series
```

The order is part of the contract: `keep_metrics`, `drop_metrics`,
`keep_if_labels`, then `drop_labels`. In `keep_if_labels`, a metric the rule
does not name is left alone, and a named metric missing the label is dropped.
An invalid regular expression is a configuration error reported by
`--config-check` with the field name.

### Protected targets (auth and TLS)

```yaml
targets:
  - name: nomad
    url: 'https://127.0.0.1:4646/v1/metrics'
    bearer_token_file: /etc/middle-monitor/nomad.token
    # bearer_token: '${NOMAD_TOKEN}'
    # basic_auth: { username: ops, password_file: /etc/middle-monitor/pw }
    headers:
      X-Scope-OrgID: demo
    tls_config:
      insecure_skip_verify: true
      # ca_file / cert_file / key_file for a private CA or mTLS
      # server_name: nomad.internal
```

`*_file` values are read again on every scrape, so a rotated token is picked up
without a restart. `${VAR}` is expanded from the process environment in
`bearer_token`, `basic_auth` and `headers` only. `cert_file` and `key_file` go
together; half a pair is rejected at validation.

### Parameterized targets (probers)

Blackbox, script or DNS exporters are told what to probe in the URL:

```yaml
targets:
  - name: speedtest
    url: 'http://127.0.0.1:9469/probe'
    params: { script: speedtest } # scalar: fixed parameter
    interval: 3600

  - name: dns_query
    url: 'http://127.0.0.1:15353/query'
    params: { module: [custom_dns1, cloudflare_dns1, quad9_dns1] }
    params_matrix: { query_name: [gmail.com, outlook.com] }
    # 3 x 2 = 6 targets, each with its own loop and labels
```

A list produces one target per value, crossed with the other lists. The values
used are attached as labels, otherwise the results could not be told apart. A
label set by hand wins over one derived from a parameter, and a query string
already in `url` is kept.

### Configuration fragments

```yaml
scrape:
  include: /etc/middle-monitor/scrape.d/*.yaml
  labels: # applied to every target of this agent
    cluster: prod
```

Each fragment holds a list of targets or a `targets:` block. Fragments are
merged with `targets` and deduplicated by name, the main file winning. A
pattern matching no file is not an error, since the agent is usually installed
before the roles that drop their fragments.

### Configuration served by the platform

```yaml
remote_config:
  enabled: false
  interval: 300 # seconds between refreshes
```

When enabled, the agent fetches a scrape fragment for its host using its
install token. The fragment is set through the API and validated before it is
stored:

```bash
curl -X PUT "https://YOUR_API_URL/api/v1/organizations/acme/hosts/42/agent-config" \
  -H "Authorization: Bearer mm_YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"scrape_config": "- name: redis\n  url: http://localhost:9121/metrics\n"}'
```

Off by default on purpose: a binary upgrade must never make an agent start
taking instructions from the network. A local target wins over a served one
with the same name, an invalid served fragment is ignored, and an unreachable
platform leaves the agent scraping what it already has.

### Service discovery (Nomad, DNS SRV, http_sd)

```yaml
scrape:
  enabled: true
  discovery_interval: 30

  # One block per family of services: each one decides where it serves metrics.
  nomad:
    - address: 'https://10.0.1.10:4646'
      namespace: '' # empty: Nomad default namespace
      region: '' # empty: region of the contacted server
      token_file: ''
      tag: middle-monitor # only services with this tag; empty: all
      service: '^(web|worker|cache-exporter)$'
      metrics_path: /metrics
      scheme: http # scheme of the discovered targets, not of the Nomad API
      labels:
        project: platform
      tls_config: # used for the Nomad API, not the targets
        insecure_skip_verify: true
      tag_labels: # tag "fqdn:app.example.com" -> label fqdn
        - prefix: 'fqdn:'
          label: fqdn
    - address: 'http://localhost:4646'
      service: '^edge-router$'
      port: 8081 # overrides the port registered in Nomad

  # Any endpoint answering in the Prometheus http_sd format
  http_sd:
    - url: 'http://127.0.0.1:8500/targets.json'
      labels:
        source: internal-sd

  srv:
    - name: '_metrics._tcp.service.consul'
      metrics_path: /metrics
      scheme: http
```

Targets are refreshed every `discovery_interval` seconds and reconciled: a new
instance gets a scrape loop, a vanished one has its loop stopped. When a source
is unreachable for a cycle, current targets are kept rather than removed, since
a gap in the data would look like a real outage. `http_sd` honors the reserved
`__metrics_path__` and `__scheme__` labels.

Labels added to discovered targets: `nomad_service`, `nomad_job`, `nomad_alloc`,
`nomad_namespace` and the configured `tag_labels` (Nomad), `srv_record` (DNS SRV),
the non-reserved source labels (http_sd), and `instance` (all).

## Exposing metrics

```yaml
expose:
  enabled: true
  listen: '127.0.0.1:9099'
  path: /metrics
  staleness: 300 # seconds; a series not refreshed for this long is removed
```

The agent serves its system metrics and everything it scraped, so an existing
Prometheus can keep scraping it during a migration. Disabled by default and
bound to localhost. Each sample carries the time the agent read it, and a stale
series disappears instead of repeating its last value. The endpoint keeps
serving when Middle Monitor is unreachable.

## How it works

1. At startup the agent registers its host; the API creates the host and one
   check per enabled metric.
2. Every `interval` seconds it collects CPU (usage and load average), memory
   and disk usage, and network throughput.
3. The API stores the values and sets each check status from its thresholds
   (critical at 90% by default). A host that has sent nothing for 5 minutes is
   shown as unknown.

## Troubleshooting

**systemd reports `status=203/EXEC`**: systemd cannot run the binary. Check
that the symlink points at an executable built for this architecture:

```bash
readlink -f /usr/local/bin/middle-monitor-agent
file "$(readlink -f /usr/local/bin/middle-monitor-agent)"; uname -m
sudo chmod +x /usr/local/bin/middle-monitor-agent-*
```

**No data in the UI**: check the service is running
(`systemctl status middle-monitor-agent`), that `api.url` is reachable from the
host, and read the agent logs (`journalctl -u middle-monitor-agent -f`, or
`~/Library/Logs/middle-monitor/` on macOS). On Linux the agent needs to read
`/proc`.

**Scraped metrics do not arrive**: check `scrape.enabled: true` and that the
agent was reloaded. The agent logs one line per target and cycle with the number
of samples scraped and exported. Make sure the target answers from the host:
`curl http://localhost:9100/metrics`.

**The agent's own /metrics is empty**: a series appears after its first
successful scrape, and is removed after `expose.staleness` seconds without a
refresh.
