# DigitalOcean VPS deployment

This deployment runs the meeting assistant as a long-lived Go server behind Caddy.
Caddy terminates HTTPS/WSS, and Docker publishes a small UDP range for WebRTC media.

## Droplet requirements

- Ubuntu 24.04 LTS or 22.04 LTS.
- TCP ports 80 and 443 open.
- UDP ports 40000-40100 open.
- A public IPv4 address.
- A DNS host that points at the Droplet. For a quick demo, use `PUBLIC_IP.nip.io`.

## One-time Droplet setup

SSH into the Droplet and run:

```bash
sudo ./deploy/digitalocean/bootstrap-ubuntu.sh
```

Copy `.env.example` to `.env`:

```bash
cd deploy/digitalocean
cp .env.example .env
```

Edit `.env`:

```bash
OPENAI_API_KEY=sk-proj-...
MEETING_MEMORY_PATH=/app/data/meeting-memory.jsonl
PION_NAT1TO1_IP=<droplet-public-ip>
PION_UDP_PORT_RANGE=40000-40100
MEETING_HOST=<droplet-public-ip>.nip.io
```

For a real domain, set `MEETING_HOST` to the domain after creating an A record that points at the Droplet.

## Launch

From the repo root on the Droplet:

```bash
cd deploy/digitalocean
docker compose up -d --build
```

The room will be available at:

```text
https://$MEETING_HOST
```

Open the URL, choose a listed participant name, enter the room password, click Join room, and allow camera and microphone access. Other participants can join the same URL natively in the browser.

## Operations

View logs:

```bash
docker compose logs -f
```

Restart after code changes:

```bash
docker compose up -d --build
```

Stop:

```bash
docker compose down
```

This demo has a lightweight room gate enforced by the server-side participant/password check. Treat it as a meeting-room passcode, not as full identity or account authentication.
