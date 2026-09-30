# MasterMC

A lightweight web panel for hosting a Minecraft server on any Linux machine, with or without a desktop (headless).
It is a single binary with no dependencies, and the web interface runs on **port 7777**.

## Features
- **Install:** Vanilla or Paper (pick a version), or any download link to a server jar (Purpur, Fabric, Velocity, …). After installing, the server starts once automatically so all files get generated.
- **Console:** live output and command input with history (↑/↓)
- **Files:** browse, edit in the browser (Ctrl+S), upload (including whole folders via drag & drop), download (folders as ZIP), rename/move, delete, extract ZIPs
- **Java:** install and remove any Temurin version (8, 11, 17, 21, 25, …). Pick the active Java yourself, or set it to “Automatic” and the matching version is downloaded when needed.
- **Dashboard:** status, CPU/RAM, uptime, start/stop/restart/kill
- **Settings:** RAM, JVM arguments (including Aikar's flags), autostart, automatic restart after a crash, password

## Getting started
```sh
./mastermc                     # panel on http://<IP>:7777
./mastermc --port 8080 --data /srv/mc --bind 127.0.0.1
./mastermc --reset-password    # generate a new random password
```
On first start an admin password is generated. It is printed to the terminal and saved in `mastermc-data/initial-password.txt`. Change it in the settings after your first login.

All data lives in the data directory (default `./mastermc-data`):
```
mastermc-data/
├── config.json   panel settings
├── server/       Minecraft server (world, configs, plugins …)
└── java/         Java versions installed by the panel
```

## Building
Requires Go ≥ 1.24.
```sh
./build.sh        # → dist/mastermc-linux-amd64, dist/mastermc-linux-arm64
go build .        # for the current system only
```

## Running as a service (headless)
See `mastermc.service`. When the panel is stopped (Ctrl+C / systemctl stop), the Minecraft server is shut down cleanly first.

## Notes
- The Minecraft port (default 25565) must be opened in your firewall and router, and port 7777 as well if you want to reach the panel from outside.
- The panel only speaks HTTP. If you expose it to the internet, put a reverse proxy with HTTPS in front of it (e.g. Caddy).
