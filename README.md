# shark-pool

Multi-exit **Surfshark VPN proxy pool** for localhost. Each exit is an independent
OpenVPN tunnel that publishes:

| Port (default US / UK) | Service |
|---|---|
| `8888` / `8889` | HTTP/HTTPS proxy (CONNECT) |
| `1080` / `1081` | SOCKS5 proxy |
| `8000` / `8001` | Per-exit control API |
| `8100` | Pool registry + 9router export |

Built for routing **selected** app traffic (e.g. [9Router](https://github.com/tommyyzhao/9router)
provider connections) through distinct exit IPs while leaving the rest of the machine alone.

## Prerequisites

- Docker + Docker Compose
- A Surfshark subscription
- **Service credentials** (not your account email/password):
  [my.surfshark.com](https://my.surfshark.com/) → VPN → Manual Setup → OpenVPN / IKEv2 credentials
- OpenVPN `.ovpn` files from the same Manual Setup page

## Quick start

```bash
git clone https://github.com/tommyyzhao/shark-pool.git
cd shark-pool

cp .env.example .env
# edit .env → SURFSHARK_USERNAME / SURFSHARK_PASSWORD

# one (or more) .ovpn per exit directory
# configs/us/us-nyc.prod.surfshark.com_udp.ovpn
# configs/uk/uk-lon.prod.surfshark.com_udp.ovpn

docker compose up -d --build
```

Verify:

```bash
curl -s http://127.0.0.1:8000/api/v1/status | jq
curl -s http://127.0.0.1:8100/api/v1/9router/export | jq
curl -x http://127.0.0.1:8888 -s https://api.ipify.org
curl --socks5 127.0.0.1:1080 -s https://api.ipify.org
```

## 9Router integration

9Router stores each pool entry as `{ name, proxyUrl, type: "http", ... }` and tests it
with an HTTP CONNECT agent. shark-pool exports exactly that shape.

1. Confirm the pool is up: `curl -s http://127.0.0.1:8100/api/v1/9router/export`
2. In 9Router dashboard → **Proxy Pools** → add each pool (or copy from export):

| Name | Proxy URL | Type |
|---|---|---|
| `shark-us` | `http://127.0.0.1:8888` | http |
| `shark-uk` | `http://127.0.0.1:8889` | http |

3. Bind a provider connection to a pool (or enable rotation across ≥2 pools).

Per-exit export is also available:

```bash
curl -s http://127.0.0.1:8000/api/v1/9router/export
curl -s http://127.0.0.1:8001/api/v1/9router/export
```

HTTP is primary for 9router compatibility (undici `ProxyAgent` / `type: http`).
SOCKS5 ports are published for Telegram, Firefox, curl, etc.

## Architecture

```
┌─────────────────────┐     ┌─────────────────────┐
│  shark-us           │     │  shark-uk           │
│  OpenVPN  → tun0    │     │  OpenVPN  → tun0    │
│  HTTP :8888         │     │  HTTP :8889         │
│  SOCKS5 :1080       │     │  SOCKS5 :1081       │
│  API :8000          │     │  API :8001          │
└─────────┬───────────┘     └─────────┬───────────┘
          │                           │
          └────────────┬──────────────┘
                       ▼
              shark-registry :8100
              GET /api/v1/9router/export
```

One container per exit. A thin Go binary (`shark-pool`) supervises OpenVPN and runs
the local proxies + control API. A third container polls exits and serves a combined
9router export.

## API

### Exit (`:8000` / `:8001`)

| Method | Path | Description |
|---|---|---|
| GET | `/health` | 200 if tunnel up, 503 otherwise (API still up) |
| GET | `/api/v1/status` | connected, server, exit IP, proxy URLs |
| POST | `/api/v1/reconnect` | restart OpenVPN |
| GET | `/api/v1/9router/export` | paste-ready pool entry |

### Registry (`:8100`)

| Method | Path | Description |
|---|---|---|
| GET | `/health` | registry liveness |
| GET | `/api/v1/status` | all exits |
| GET | `/api/v1/9router/export` | all pools for 9router |

## Configuration

| Variable | Default | Description |
|---|---|---|
| `SURFSHARK_USERNAME` | — | Service credential username |
| `SURFSHARK_PASSWORD` | — | Service credential password |
| `EXIT_NAME` | `exit` | Label (`us`, `uk`, …) |
| `VPN_CONFIG_DIR` | `/vpn/configs` | Directory of `.ovpn` files |
| `VPN_CONFIG` | first `.ovpn` | Explicit config path |
| `HTTP_PORT` | `8888` | HTTP proxy port **inside** the container |
| `SOCKS_PORT` | `1080` | SOCKS5 port inside the container |
| `API_PORT` | `8000` | Control API port inside the container |
| `PUBLIC_HOST` | `127.0.0.1` | Host used in exported proxy URLs |
| `AUTO_RECONNECT` | `true` | Reconnect when tunnel drops |
| `CONNECT_TIMEOUT_SEC` | `75` | OpenVPN connect wait |
| `KILL_SWITCH` | `false` | Reserved |

Registry mode: `SHARK_POOL_MODE=registry` + `REGISTRY_EXITS=name|healthURL|publicHTTP[|publicSOCKS],…`

## Adding exits

1. Create `configs/<name>/` with an `.ovpn`.
2. Duplicate the `us` service in `docker-compose.yml` with new host ports.
3. Append the exit to `REGISTRY_EXITS` using the **host** proxy port.

## Notes

- Credentials are **manual-setup service credentials**, not account login.
- `.ovpn` files and `.env` are gitignored — never commit them.
- Not affiliated with Surfshark.

## License

MIT
