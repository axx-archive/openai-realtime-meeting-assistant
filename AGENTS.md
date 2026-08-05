# MeetingAssist Repo Notes

## Live Deployment

This repo's production-style live app is hosted directly on a DigitalOcean VPS, not Vercel.
Pushing to GitHub does not automatically update the running app because the VPS copy at
`/opt/meetingassist` is not currently a git checkout.

- DigitalOcean droplet: `meetingassist-demo`
- Public IP: `146.190.171.224`
- SSH user: `root`
- Live hosts: `thebonfire.xyz`, `146.190.171.224.nip.io`
- Legacy mutable app path: `/opt/meetingassist` (runtime secrets only; never deploy from this tree)
- Exact releases path: `/opt/meetingassist-releases/<full-commit-sha>`
- Active release ledger: `/opt/meetingassist-releases/active-release.json`
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
2. Use the exact-release procedure in `deploy/digitalocean/README.md`: prepare a clean,
   reviewed full commit archive locally, upload it under
   `/opt/meetingassist-releases/<sha>/`, build only that archive, and activate it through
   the verified tool retained in the currently active rollback release.
3. Never rsync application files into `/opt/meetingassist` and never run
   `docker compose up -d --build` there. That mutable route bypasses the release receipt
   and makes `active-release.json` disagree with the serving containers.
4. Before activation, require all of the following to agree:
   - the ledger's active release directory and receipt;
   - every running Compose service image ID;
   - the public `/healthz` and `/readyz` release identity;
   - the retained rollback bundle and images.
5. Allow the receipted five-minute first-start window to finish. A container in Docker's
   `starting` state is not a failed release unless the bounded activation command fails.
6. After activation, run the retained release verifier and require
   `verified-local-unsigned` for the exact target commit. Keep the prior release directory
   and images intact for rollback.

If the ledger and serving containers ever disagree, stop the normal activation path. Do
not switch application images merely to make a stale ledger true, and do not edit
production data. First identify the exact serving receipt. Reconcile the ledger only after
the entire already-serving bundle passes the same image, runtime, renderer, Caddy, health,
and readiness checks as a normal activation; preserve a private copy of the prior ledger.

The VPS does not have Go installed directly, so run `go test ./...` locally before deployment.
The exact Docker build compiles the Go binary from the reviewed archive inside the image.
