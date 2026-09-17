# shark-pool

Multi-location **Surfshark VPN proxy pool** for localhost. Each country container loads
**every** `.ovpn` in its config folder and runs one concurrent tunnel + HTTP + SOCKS5
proxy per location. A registry exports paste-ready [9Router](https://github.com/tommyyzhao/9router)
proxy-pool entries for every live location.

| Service | Default |
|---|---|
| US locations | HTTP `8888+` · SOCKS5 `1080+` · API `:8000` |
| UK locations | HTTP `8920+` · SOCKS5 `1120+` · API `:8001` |
| Registry | `:8100` — `/api/v1/9router/export` |

## How it works

```
configs/us/*.ovpn  (24 cities)     configs/uk/*.ovpn  (4 cities)
        │                                  │
        ▼                                  ▼
   shark-us container                shark-uk container
   tun0+tun1+… + SO_BINDTODEVICE     same
   one HTTP/SOCKS port per city
        │                                  │
        └────────────┬─────────────────────┘
                     ▼
              shark-registry :8100
         GET /api/v1/9router/export
```

Each tunnel gets a unique `tunN`, local UDP `--lport`, and its proxy pins outbound
sockets to that device — so every location has its **own exit IP**.

## Quick start

```bash
git clone https://github.com/tommyyzhao/shark-pool.git
cd shark-pool
cp .env.example .env
# SURFSHARK_USERNAME / SURFSHARK_PASSWORD = manual-setup service credentials

# drop .ovpn files into configs/us/ and configs/uk/
docker compose up -d --build
```

Verify:

```bash
curl -s http://127.0.0.1:8000/api/v1/status | jq '.connected_count, .server_count'
curl -s http://127.0.0.1:8100/api/v1/9router/export | jq '.count'
curl -x http://127.0.0.1:8888 -s https://api.ipify.org
curl -x http://127.0.0.1:8894 -s https://api.ipify.org   # different city → different IP
```

## 9Router integration

```bash
curl -s http://127.0.0.1:8100/api/v1/9router/export
```

Returns one entry per live location:

```json
{ "name": "shark-us-nyc", "proxyUrl": "http://127.0.0.1:8905", "type": "http", "isActive": true }
```

Import into **9Router → Proxy Pools** (type `http`), then rotate across ≥2 pools.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `SURFSHARK_USERNAME` | — | Service credential username |
| `SURFSHARK_PASSWORD` | — | Service credential password |
| `EXIT_NAME` | `exit` | Country label (`us`, `uk`) |
| `VPN_CONFIG_DIR` | `/vpn/configs` | Folder of `.ovpn` files |
| `HTTP_PORT` | `8888` | Base HTTP port (location *i* → base+*i*) |
| `SOCKS_PORT` | `1080` | Base SOCKS5 port |
| `MAX_SERVERS` | `0` (all) | Cap concurrent locations |
| `PUBLIC_HOST` | `127.0.0.1` | Host in exported proxy URLs |
| `AUTO_RECONNECT` | `true` | Reconnect dropped tunnels |

Registry: `REGISTRY_EXITS=us|http://us:8000,uk|http://uk:8000`

## API

### Country (`:8000` / `:8001`)

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/status` | All locations + exit IPs |
| GET | `/api/v1/9router/export` | Paste-ready pools |
| POST | `/api/v1/reconnect?id=us-nyc` | Reconnect one or all |

### Registry (`:8100`)

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/status` | Flattened locations |
| GET | `/api/v1/9router/export` | All pools for 9router |

## Notes

- Credentials are **manual-setup service credentials**, not account login.
- Surfshark may cap concurrent OpenVPN sessions; some cities can flap — auto-reconnect retries.
- `.ovpn` and `.env` are gitignored.
- Not affiliated with Surfshark.

## License

MIT
