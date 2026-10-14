# Hosting a public TrendPilot

How the public "Add to Discord" TrendPilot runs, so anyone can run their own instance for a community.

## What one instance does

- Serves any number of Discord servers. Each sets its own `/mychannel`, `/timezone`, channels and Twitch sign-in.
- Shares the game and YouTube scans across all servers: one scan every 10 minutes, whatever the number of servers.
- Checks every followed creator's live status in **one** batched Twitch request per minute (100 channels per request), with one read-only chat connection per creator.
- Limits `/suggest` to one request per person every 20 s and `TRENDPILOT_SUGGEST_PER_DAY` (default 50) per server, with at most 2 AI calls at once.
- Follows at most 500 creators per instance.

## Requirements

A tiny Linux VM is enough. The reference instance runs on **1 vCPU / 512 MB** next to other services, and uses about 7–17 MB of memory.

## Install

```bash
# 1. Service account and folders
useradd --system --home-dir /var/lib/trendpilot --shell /usr/sbin/nologin trendpilot
install -d -o trendpilot -g trendpilot -m 0700 /var/lib/trendpilot
install -d -m 0755 /opt/trendpilot

# 2. Binary (check it against checksums.txt from the same release)
V=v0.2.0
curl -fsSLO https://github.com/Mathiew1618/trendpilot/releases/download/$V/trendpilot_${V}_linux_amd64.tar.gz
curl -fsSLO https://github.com/Mathiew1618/trendpilot/releases/download/$V/checksums.txt
sha256sum --ignore-missing -c checksums.txt
tar xzf trendpilot_${V}_linux_amd64.tar.gz
install -m 0755 trendpilot_${V}_linux_amd64/trendpilot /opt/trendpilot/trendpilot

# 3. Settings (root-only): see trendpilot.env.example. Leave DISCORD_GUILD_ID empty
#    so commands register in every server, and set TRENDPILOT_DATA_DIR=/var/lib/trendpilot.
install -d -m 0750 /etc/trendpilot
nano /etc/trendpilot/trendpilot.env && chmod 600 /etc/trendpilot/trendpilot.env

# 4. Service (sandboxed, 96 MB memory cap, 50% CPU, low priority)
cp deploy/trendpilot.service /etc/systemd/system/
systemctl daemon-reload && systemctl enable --now trendpilot
journalctl -u trendpilot -f
```

In the Discord Developer Portal, turn on **Bot → Public Bot** so other people can add it.

## Update

```bash
V=v0.2.1   # new version
# download and check as in step 2, then:
install -m 0755 trendpilot_${V}_linux_amd64/trendpilot /opt/trendpilot/trendpilot
systemctl restart trendpilot
```

Settings, viewer history and Twitch sign-ins in `/var/lib/trendpilot` are kept across updates.

## What's stored

All of it lives in `/var/lib/trendpilot`, readable only by the service account:

| File | Contents |
|---|---|
| `settings.json` | Per-server channel choices, time zone, Twitch channel name, muted alerts |
| `twitch-users/<server id>.json` | A creator's Twitch sign-in (clips:edit, channel:manage:raids), if they ran `/connect-twitch` |
| `history.json` | Hourly viewer averages per game: no personal data |

`/reset` deletes a server's settings and sign-in. Back these files up if you move servers.

## Health

- `curl 127.0.0.1:8790/api/jobs`: each background job's last run, duration and error
- `curl 127.0.0.1:8790/api/usage`: CPU, memory and data use
- `journalctl -u trendpilot`: logs. A `usage` line is written every hour.
