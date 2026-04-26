# Deploy Rush Delivery — Oracle Cloud (free)

End-to-end production deployment of the backend using only free tiers that don't expire.

```
                       ┌────────────────────────────┐
  API client ─ HTTPS ─►│   Oracle Cloud VM          │
  (curl / Postman /    │                            │
   wscat / any app)    │  Caddy (auto-HTTPS)        │
                       │    ├─► order-service:8080  │
                       │    ├─► rider-service:8081  │
                       │    ├─► location-service:…  │
                       │    └─► notification:…      │
                       │                            │
                       │  docker compose:           │
                       │    postgres · redis · kafka│
                       │    + 2 consumer services   │
                       └────────────────────────────┘
```

---

## 1. Prerequisites

- GitHub account with this repo (you already have it)
- A free Cloudflare or DuckDNS account (for a domain)
- SSH client — on Windows: `ssh` is built into PowerShell, or use Termius

---

## 2. Oracle Cloud — Always Free VM

Oracle gives away **4 ARM cores + 24 GB RAM** permanently (no expiry, no card hold).
That's enough to run the entire stack including Kafka on one machine.

### 2.1 Sign up

1. Go to <https://cloud.oracle.com/> → **Start for free**
2. Pick a region physically close to you (e.g. Mumbai `ap-mumbai-1` if you're in India)
3. They'll ask for a credit card for identity verification only — you will **not** be charged on Always Free resources. To stay safe, set up a billing alert later (step 2.6).

### 2.2 Create the VM (Compute → Instances → Create)

- **Shape**: change to **Ampere** → `VM.Standard.A1.Flex` → **4 OCPU, 24 GB RAM** (this is the Always Free limit, use all of it)
- **Image**: Canonical Ubuntu 22.04
- **Networking**: default VCN, assign public IP
- **SSH**: upload your public key (or let Oracle generate — save the private key)
- **Boot volume**: 100 GB (also free up to 200 GB)
- Click **Create**

If you see *"Out of capacity"*: Oracle's ARM capacity fluctuates. Retry every 10–30 min or pick a less-busy region (Phoenix, Frankfurt often work).

### 2.3 Open firewall ports

The VM's **Virtual Cloud Network** has a Security List that blocks everything except SSH by default. Add ingress rules for:

| Port | Protocol | Why |
|---|---|---|
| 80  | TCP | HTTP (Caddy needs this for ACME challenge) |
| 443 | TCP | HTTPS |

Navigate: **Networking → Virtual Cloud Networks → your VCN → Security Lists → Default → Add Ingress Rules**. Source CIDR `0.0.0.0/0`.

Also inside Ubuntu itself:
```bash
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo iptables -I INPUT -p tcp --dport 80 -j ACCEPT    # Oracle's images have iptables rules too
sudo iptables -I INPUT -p tcp --dport 443 -j ACCEPT
sudo netfilter-persistent save
```

### 2.4 SSH in + install Docker

From your laptop:
```bash
ssh -i ~/.ssh/your-key ubuntu@<PUBLIC_IP>
```

Once in:
```bash
sudo apt update && sudo apt upgrade -y
sudo apt install -y docker.io docker-compose-plugin git
sudo usermod -aG docker ubuntu
# Log out & back in for group change to take effect
exit
```

Re-SSH, then verify: `docker version && docker compose version`.

### 2.5 Clone and run the backend

```bash
git clone https://github.com/Kush8459/rush-delivery-engine.git
cd rush-delivery-engine

# Build all 6 service images for ARM64 (your VM's architecture)
docker compose --profile app build

# Bring up infra + app services together
docker compose --profile app up -d

# Verify
docker compose ps
```

All 9 containers should be `Up`. If Kafka is `Restarting`, wait a minute — it needs more memory on first boot.

### 2.6 Billing safety

**Oracle → Governance → Billing → Budgets** → set a $1 monthly budget with 100% alert email. Always Free usage never hits this; anything that does is unauthorized and you'll know immediately.

---

## 3. Domain + automatic HTTPS (Caddy)

Even a backend-only deployment wants HTTPS — JWTs in headers, WebSocket auth, basic hygiene. Caddy fronts the services and handles TLS automatically.

### 3.1 Get a free subdomain (DuckDNS — easiest)

1. <https://www.duckdns.org/> → sign in with GitHub/Google
2. Create a subdomain, e.g. `rush-delivery.duckdns.org`
3. Enter your Oracle VM's public IP → **Update IP**
4. Copy your DuckDNS **token** (shown on the dashboard)

Keep the IP fresh automatically (install on the VM):
```bash
mkdir -p ~/duckdns && cd ~/duckdns
echo 'url="https://www.duckdns.org/update?domains=rush-delivery&token=YOUR_TOKEN&ip="' > duck.sh
echo 'curl -k -o ~/duckdns/duck.log -K ~/duckdns/duck.sh' >> duck.sh
chmod +x duck.sh
(crontab -l 2>/dev/null; echo "*/5 * * * * ~/duckdns/duck.sh >/dev/null 2>&1") | crontab -
```

### 3.2 Front the services with Caddy

Caddy handles TLS certs automatically via Let's Encrypt. Add a new compose service.

Create `~/rush-delivery-engine/Caddyfile` on the VM:
```caddy
rush-delivery.duckdns.org {
    # REST APIs
    handle /api/v1/orders/* {
        reverse_proxy order-service:8080
    }
    handle /api/v1/auth/* {
        reverse_proxy order-service:8080
    }
    handle /api/v1/riders/* {
        reverse_proxy rider-service:8081
    }

    # WebSockets
    handle /ws/order/* {
        reverse_proxy notification-service:8085
    }
    handle /ws/rider/* {
        reverse_proxy location-service:8083
    }

    # Health / metrics (optional, lock down in real prod)
    handle /healthz {
        reverse_proxy order-service:8080
    }
}
```

Add to `docker-compose.yml` under the `app` profile:
```yaml
  caddy:
    profiles: [app]
    image: caddy:2
    ports: ["80:80", "443:443"]
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy-data:/data
      - caddy-config:/config
    depends_on:
      - order-service
      - rider-service
      - location-service
      - notification-service
    restart: unless-stopped

volumes:
  pgdata:
  grafana-data:
  caddy-data:
  caddy-config:
```

Apply:
```bash
docker compose --profile app up -d
docker logs rush-delivery-engine-caddy-1 -f   # watch for "certificate obtained"
```

First cert takes 20-60 seconds. Once you see `serving https`, visit `https://rush-delivery.duckdns.org/healthz` — should return `ok`.

### 3.3 CORS (only if a browser client will call the API)

If everything calls the API via `curl`/Postman/a server-side client, skip this — CORS is a browser-only concern.

If you later point a browser app at this backend, add `github.com/go-chi/cors` in `cmd/order-service/main.go` and `cmd/rider-service/main.go`:

```go
import "github.com/go-chi/cors"

// In run():
r.Use(cors.Handler(cors.Options{
    AllowedOrigins: []string{"https://your-client-origin.example"},
    AllowedMethods: []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
    AllowedHeaders: []string{"Content-Type", "Authorization", "X-Customer-Id", "Idempotency-Key"},
    AllowCredentials: false,
    MaxAge: 300,
}))
```

(WebSocket upgrader already has `CheckOrigin: return true` so WS is fine as-is. Tighten it for prod.)

---

## 4. Verify end-to-end

Against `https://rush-delivery.duckdns.org`:

```bash
# 1. Register a rider
curl -s -X POST https://rush-delivery.duckdns.org/api/v1/riders \
  -H 'Content-Type: application/json' \
  -d '{"name":"Ada","phone":"+911234567890"}'

# 2. Put rider online
curl -s -X PATCH https://rush-delivery.duckdns.org/api/v1/riders/<rider_id>/status \
  -H 'Content-Type: application/json' \
  -d '{"status":"AVAILABLE","lat":28.6139,"lng":77.2090}'

# 3. Login → token
TOKEN=$(curl -s -X POST https://rush-delivery.duckdns.org/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"customer_id":"22222222-2222-2222-2222-222222222222"}' | jq -r .token)

# 4. Place an order
curl -s -X POST https://rush-delivery.duckdns.org/api/v1/orders \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'X-Customer-Id: 22222222-2222-2222-2222-222222222222' \
  -d '{"customer_id":"22222222-2222-2222-2222-222222222222","restaurant_id":"33333333-3333-3333-3333-333333333333","total_amount":249.50,"delivery_address":"Test","pickup_lat":28.6139,"pickup_lng":77.2090,"dropoff_lat":28.6200,"dropoff_lng":77.2150}'

# 5. Customer WebSocket — watch live events
wscat -c wss://rush-delivery.duckdns.org/ws/order/<order_id>

# 6. Rider WebSocket — push synthetic GPS
wscat -c wss://rush-delivery.duckdns.org/ws/rider/<rider_id>
> {"type":"location_update","payload":{"lat":28.6145,"lng":77.2095}}
```

If anything fails, tail Caddy + service logs: `docker compose logs -f caddy order-service`.

---

## 5. Updating after you push code changes

From your laptop:
```bash
git push
```

Then on the VM:
```bash
ssh ubuntu@<VM_IP>
cd rush-delivery-engine
git pull
docker compose --profile app build
docker compose --profile app up -d
```

Or wire up a GitHub Actions workflow that SSHes in and runs the above — beyond scope here.

---

## 6. Troubleshooting

| Symptom | Check |
|---|---|
| `ssh: Connection refused` | Security List missing port 22 rule; or VM still booting |
| Caddy logs: `no IP address` | DuckDNS hasn't propagated — wait 1-5 min |
| Caddy logs: `rate limit` | Let's Encrypt rate-limited you (too many cert attempts); wait an hour |
| API `401 Unauthorized` | JWT missing/expired — hit `/api/v1/auth/login` again |
| API `429 Too Many Requests` | Rate limiter — 5 orders/min per `X-Customer-Id` |
| Kafka `OOMKilled` | Single-node cluster on 24 GB should be fine; check `docker stats` |
| Oracle "out of capacity" | Retry later or try a different region |
| Ports 80/443 blocked | Open in Security List **and** in `ufw` + `iptables` (Oracle Ubuntu images have both) |

---

## 7. Cost

Everything listed is free:

| Service | Free tier | Caveat |
|---|---|---|
| Oracle Ampere A1 VM | 4 OCPU + 24 GB forever | Account must have activity every 30 days or instances may be reclaimed |
| DuckDNS | Unlimited subdomains | None |
| Let's Encrypt (via Caddy) | Unlimited certs | 50/week/domain rate limit |

Total: **$0/month** if you stay within limits.

---

## 8. Next steps once this is live

- Add **Cloudflare** in front of DuckDNS for DDoS protection + caching
- Add **GitHub Actions auto-deploy** to the VM on push
- Enable **Prometheus + Grafana** on the VM (add `--profile obs`), expose via Caddy at `/grafana` with basic auth
- Rotate the JWT secret (currently the default dev value) — set `DE_AUTH_JWT_SECRET` as a Docker secret
