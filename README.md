# Game Server Snooze

This tool auto-starts and auto-stops a game server. Originally created for Palworld, but designed with a modular approach that allows adding support for other games. It listens on a UDP port and, when it detects a bound session, it ensures the server is running. After all sessions end, it waits a configurable delay before stopping the server.

Each game requires its own module that implements detection of start/close signatures in the game's network protocol. Currently supported games:
- Palworld

## How It Works
- A UDP proxy is started on a local port (configured via config.yaml or environment variables).
- When a client sends a "start" signature, a new session is created. If no sessions existed, the server is started automatically.
- If all sessions close, the server stops after a delay set by "auto_stop_delay."

## Configuration
You can configure the application in two ways:

1. Environment Variables:

| Variable | Description | Default |
|----------|-------------|---------|
| **Network Settings** |||
| LISTEN_ADDR | Local address to listen on | ":8211" |
| SERVER_ADDR | Game server address to forward to | "" |
| MAX_PACKET_SIZE | Maximum UDP packet size. The default value is most of the times a big overkill, just for safety. For most of the games it's fine to put it at 4096, to optimize RAM usage  | 65507 |
| **Game Settings** |||
| GAME | Game type (currently only "palworld") | "palworld" |
| MAX_SESSIONS | Maximum number of concurrent sessions | 32 |
| **Timing Settings** |||
| AUTO_STOP_DELAY | How long to wait after last session before stopping server | "1m" |
| IDLE_TIMEOUT | How long before considering a session idle | "30s" |
| **Pterodactyl Settings** |||
| PTERO_BASE_URL | URL of your Pterodactyl panel | "" |
| PTERO_API_TOKEN | Your Pterodactyl API token | "" |
| PTERO_SERVER_ID | ID of the game server | "" |
| **Misc Settings** |||
| LOG_LEVEL | Logging level (debug, info, warn, error, fatal) | "info" |

2. config.yaml:
   ```yaml
   listen_addr: ":8211"
   server_addr: ""
   max_sessions: 32
   max_packet_size: 4096
   game: "palworld"
   auto_stop_delay: 1m
   log_level: "info"
   idle_timeout: 30s

   pterodactyl:
     base_url: ""
     api_token: ""
     server_id: ""
   ```

Any environment variable overrides the matching setting in config.yaml.

## Usage (Docker)
Include the docker-compose.yml(example file is in the root of this repository) to run the container:

```bash
docker-compose up -d
```

Or, build your own image:
```bash
docker build -t my-game-server-snooze .
docker run -p 8211:8211/udp \
  -e LISTEN_ADDR=":8211" \
  -e SERVER_ADDR="192.168.50.52:8211" \
  -it my-game-server-snooze
```

Use environment variables or provide a config.yaml inside the container (`/usr/local/bin/game-server-snooze/config.yaml`).

## Usage (executable file)
It's possible to run this app on almost any platform, for example as a windows executable. 

TODO...