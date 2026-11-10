# netspeed

A fast, single-binary internet speed test for the terminal, written in Go.

It measures much more than download and upload speed:

| Metric | How it is measured |
| --- | --- |
| **Download / upload** | Parallel HTTP/1.1 streams (8 by default), each on its own TCP connection. The TCP slow-start warmup is excluded, and peak speed comes from a sliding 1 s window. |
| **Latency and jitter** | TCP handshake round trips to the test server (no root needed), with min, avg, median, p90, max and jitter. |
| **Packet loss** | ICMP echo to the test server, UDP DNS probes, or UDP echo against a self-hosted server, picked automatically. Duplicates are counted too. |
| **Loaded latency (bufferbloat)** | Latency probes keep running during the download and upload, so you see how much your queue grows under load. Graded A+ to F. |
| **Loss under load** | Packet loss probes also run during each transfer. |
| **Connection setup** | DNS lookup, TCP connect, TLS handshake, time to first byte and warm HTTP request time, plus TLS version and cipher. |
| **Who and where** | Your public IP, ASN, approximate location, the server's data center, and your local IP and address family. |
| **Quality ratings** | Streaming, gaming and video calls rated Great, Good, Fair or Poor, with the metric that limits each one. |

By default it tests against Cloudflare's global network (`speed.cloudflare.com`), so no account or API key is needed. You can also run your own server with `netspeed serve`.

```
  netspeed v0.1.0 · Cloudflare · https://speed.cloudflare.com

  Server       Cloudflare · BOM · 162.159.140.220
  Client       203.0.113.42 · AS64500 · Mumbai, IN · local 192.168.1.20 (IPv4)
  Connection   DNS 9.46 ms · TCP 2.29 ms · TLS 24.1 ms · TTFB 35.5 ms
               HTTP/1.1 · TLS 1.3 · TLS_AES_128_GCM_SHA256 · HTTP request 38.1 ms

  Latency      2.31 ms   jitter 0.46 ms
               min 2.02 ms · avg 2.55 ms · p90 2.56 ms · max 7.17 ms · 20 samples
  Packet loss  0.00 %   0 of 100 lost
               icmp to 162.159.140.220 · rtt 2.13 ms · jitter 0.15 ms
  Download     61.76 Mbps  ▅▇██▇▆▆▆▅▅▄▄▄▄▄▅▄▄▅▅▅▇▇▇███▇
               peak 94.56 Mbps · 81.8 MB · 8 streams · 10.0s
               loaded latency 13.4 ms · +11.1 ms · p90 16.9 ms · loss 11.44 %
  Upload       92.99 Mbps  █▆▆▆▆▆▆▆▆▆▆▆▅▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆
               peak 100.66 Mbps · 117.4 MB · 8 streams · 10.0s
               loaded latency 16.8 ms · +14.5 ms · p90 20.3 ms · loss 0.50 %

  Bufferbloat  A   +11.1 ms download · +14.5 ms upload
  Quality      Streaming    Great
               Gaming       Great
               Video calls  Great
  Data used    81.8 MB down · 117.4 MB up
```

While a test runs, each phase shows a live spinner, current speed, a progress bar and the current loaded ping.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/0xshubhs/internetspeedtestcli/main/install.sh | sh
```

What the script does:

1. Clones this repo into a temporary folder, or downloads the source archive when `git` is not installed.
2. Uses your Go if it is 1.26 or newer. Otherwise it downloads the current Go release from go.dev into the same temporary folder and checks it against the official SHA-256 checksum. Nothing is installed system-wide.
3. Compiles `netspeed` and installs it to `~/.local/bin`, or `/usr/local/bin` when run as root.
4. Deletes the clone, the temporary Go and its caches, then tells you if the install directory is not on your `PATH`.

It needs `git` (or `curl`/`wget`) and `tar`. It targets Linux and macOS, and Windows under Git Bash.

Options go after `sh -s --`, for example:

```sh
curl -fsSL https://raw.githubusercontent.com/0xshubhs/internetspeedtestcli/main/install.sh | sh -s -- --dir /usr/local/bin
```

| Option | What it does |
| --- | --- |
| `--version v0.1.0` | Builds a specific tag or branch instead of `main` |
| `--dir DIR` | Installs to another directory |
| `--uninstall` | Removes netspeed |
| `--local` | Builds the checkout the script sits in, when run as `./install.sh` from a clone (this is what `make install` does) |

With Go 1.26 or newer you can also run `go install github.com/0xshubhs/internetspeedtestcli/cmd/netspeed@latest`.

## Usage

| Command | What it does |
| --- | --- |
| `netspeed` | Full test, about 25 seconds |
| `netspeed -d 20s -c 16` | Longer test with more streams, for fast links |
| `netspeed --no-upload` | Skips a phase |
| `netspeed --json > result.json` | Saves the full machine-readable report |
| `netspeed --csv --csv-header >> log.csv` | Appends one row per run, good for cron |
| `netspeed --simple` | Short plain-text summary |
| `netspeed --bytes` | Shows MB/s instead of Mbps |
| `netspeed -4` | IPv4 only (`-6` for IPv6) |

### Flags

| Flag | Default | Description |
| --- | --- | --- |
| `-s, --server URL` | `https://speed.cloudflare.com` | Test server |
| `-d, --duration D` | `10s` | Length of each transfer test |
| `-c, --connections N` | `8` | Parallel TCP streams per transfer |
| `--chunk-mb N` | `25` | Size of each HTTP transfer request |
| `--latency-count N` | `20` | Idle latency samples |
| `--loss-method M` | `auto` | `auto`, `icmp`, `udp` (DNS probes), `echo` or `off` |
| `--loss-count N` | `100` | Packets in the idle loss test |
| `--loss-interval D` | `20ms` | Gap between loss probes |
| `--dns-server ADDR` | `1.1.1.1:53` | Resolver for UDP DNS probes |
| `--no-download`, `--no-upload`, `--no-latency`, `--no-loss` | | Skip a phase |
| `-4`, `-6` | | Force an address family |
| `--json`, `--csv`, `--simple` | | Output format (`--csv-header` adds the header row) |
| `--bytes` | | Show speeds in bytes per second |
| `--no-color` | | Disable colors (`NO_COLOR` is honored) |

The exit code is `0` on success, `1` if any phase failed, `2` for bad flags and `130` when interrupted. When output is not a terminal, the live progress is skipped and only final results are printed.

### Packet loss methods

With `--loss-method auto`, netspeed sends three quick probes with each method and uses the first one that gets a reply:

1. **UDP echo** when the server is a `netspeed serve` instance. This is the most accurate, because it uses the same server and path as the speed test.
2. **ICMP echo** to the test server. This works without root on Linux (through `net.ipv4.ping_group_range`, enabled by default on most distributions) and macOS, and falls back to a raw socket when running as root.
3. **UDP DNS** queries to `1.1.1.1`, which works almost everywhere.

Each probe carries a random token, so replies from other traffic are never counted.

## Self-hosted server

Test against your own VPS, home server or LAN box to separate ISP problems from Wi-Fi or local problems:

On the server:

```sh
netspeed serve --listen :8080
```

On the client:

```sh
netspeed --server http://your-server:8080
```

The server implements the same `/__down?bytes=N` and `/__up` endpoints as Cloudflare. It also runs a UDP echo on the same port, so packet loss is measured end to end over UDP. Use `--udp off` to disable the echo, `--max-bytes` to cap request sizes and `--name` to set the label clients see.

## JSON output

`--json` prints the complete report: every latency summary (`min`, `mean`, `median`, `p90`, `max`, `stddev`, `jitter`), loss counters, transfer details, bufferbloat and quality ratings. An abbreviated example:

```json
{
  "download": {
    "mbps": 93.056,
    "peak_mbps": 95.83,
    "bytes": 115780315,
    "duration_sec": 10.001,
    "streams": 8,
    "loaded_latency_ms": { "count": 32, "median": 13.4, "p90": 16.9, "jitter": 1.2 },
    "latency_increase_ms": 11.1,
    "loaded_packet_loss": { "method": "icmp", "sent": 201, "lost": 23, "loss_pct": 11.44 }
  },
  "bufferbloat": { "grade": "A", "increase_ms": 14.5 },
  "quality": [{ "use_case": "Gaming", "rating": "Great" }]
}
```

## Notes

- A full test moves roughly `speed x 20 s` of data (about 250 MB on a 100 Mbps line). Keep that in mind on metered connections, or shorten it with `-d`.
- Cloudflare rate-limits IPs that run many large tests back to back. netspeed stops with a clear message when it sees HTTP 429; wait a few minutes or use your own server.
- HTTP/2 is intentionally disabled so that every stream gets its own TCP connection, which is how multi-stream speed tests saturate a link.
- Grade thresholds for bufferbloat (latency increase under load): A+ under 5 ms, A under 30 ms, B under 60 ms, C under 200 ms, D under 400 ms, F otherwise.

## Development

| Command | What it does |
| --- | --- |
| `make test` | Runs `go vet` and the tests with the race detector |
| `make build` | Builds `./bin/netspeed` |
| `make install` | Builds this checkout and installs it with `install.sh` |
| `make dist` | Cross-compiles the release binaries into `./dist` |

## License

MIT
