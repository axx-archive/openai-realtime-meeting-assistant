# MeetingAssist Repo Notes

## Live Deployment

This repo's production-style live app is hosted directly on a DigitalOcean VPS, not Vercel.
Pushing to GitHub does not automatically update the running app because the VPS copy at
`/opt/meetingassist` is not currently a git checkout.

- DigitalOcean droplet: `meetingassist-demo`
- Public IP: `146.190.171.224`
- SSH user: `root`
- Live hosts: `thebonfire.xyz`, `146.190.171.224.nip.io`
- VPS app path: `/opt/meetingassist`
- Compose path: `/opt/meetingassist/deploy/digitalocean`
- Backups path: `/opt/meetingassist-backups`
- Compose service: `meetingassist`
- Caddy service: `caddy`

## Production Data Location (read this before touching "the board" or any prod data)

Live production data — `kanban-board.json` (the real Kanban board), `meeting-memory.jsonl`,
`users.json`, `rooms.json`, `sessions.json`, `archives/` — lives ONLY in the docker named
volume `digitalocean_meeting_data`, mounted at `/app/data` inside the containers:

- On the droplet: `/var/lib/docker/volumes/digitalocean_meeting_data/_data/`

⚠️ `/opt/meetingassist/data/` on the droplet is a STALE rsync artifact (see the
`README-NOT-LIVE-DATA.md` inside it). Its `kanban-board.json` holds only the 5 seeded demo
WebRTC cards from `initialKanbanBoardCards` in `kanban.go` — it is NOT the live board.
The local repo's `data/` directory is likewise seed/dev data, never production state.
Deploy rsyncs must keep excluding `data/`.

## Deploy Flow

When asked to deploy this repo to the VPS:

1. Commit and push local changes to `axx/main` if the user asked for a git push.
2. Back up the current VPS files before replacing them:

   ```bash
   ssh root@146.190.171.224 'cd /opt/meetingassist && backup_dir="/opt/meetingassist-backups/$(date +%Y%m%d-%H%M%S)-<reason>" && mkdir -p "$backup_dir" && cp <changed-files> "$backup_dir"/'
   ```

3. Sync changed files to the VPS:

   ```bash
   rsync -av <changed-files> root@146.190.171.224:/opt/meetingassist/
   ```

4. Rebuild and restart the live containers:

   ```bash
   ssh root@146.190.171.224 'cd /opt/meetingassist/deploy/digitalocean && docker compose up -d --build && docker compose ps'
   ```

5. Verify the deployed app:

   ```bash
   curl -fsSI --max-time 20 https://thebonfire.xyz
   curl -fsS --max-time 20 https://thebonfire.xyz | rg '<expected new code/text>'
   ssh root@146.190.171.224 'cd /opt/meetingassist/deploy/digitalocean && docker compose logs --tail=80 meetingassist'
   ```

The VPS does not have Go installed directly, so run `go test ./...` locally before deployment.
The Docker build compiles the Go binary inside the container image.
