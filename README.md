# chatto-cli

Command-line client for [Chatto](https://chatto.run) — a self-hosted team chat platform.

Connects to the Chatto GraphQL API to browse spaces and rooms, read and send messages, and stream live events via WebSocket subscription.

## Install

```sh
go install github.com/teal-bauer/chatto-cli@latest
```

Or clone and build:

```sh
git clone https://github.com/teal-bauer/chatto-cli
cd chatto-cli
go build -o chatto .
```

## Quick start

```sh
chatto login                    # authenticate and save session
chatto spaces                   # list spaces you have access to
chatto rooms myspace            # list rooms in a space
chatto messages myspace general # show recent messages
chatto watch myspace            # stream live events (Ctrl+C to stop)
chatto send myspace general hello world
```

## Commands

| Command | Description |
|---------|-------------|
| `login [profile]` | Authenticate and store session |
| `logout [profile]` | Remove stored session |
| `profiles` | List saved profiles |
| `spaces` | List spaces |
| `join-space <space>` | Join a space |
| `leave-space <space>` | Leave a space |
| `rooms <space>` | List rooms in a space |
| `join <space> <room>` | Join a room |
| `leave <space> <room>` | Leave a room |
| `messages <space> <room>` | Show recent messages |
| `send <space> <room> <text…>` | Send a message |
| `watch <space>` | Stream live events |
| `me` | Show current user |
| `repl` | Interactive shell |

### `messages` flags

- `-n N` — number of messages to fetch (default 20)
- `--since <event-id>` — show messages after this event ID

### `watch` flags

- `--room <name>` — filter events to one room
- `--history N` — show last N messages before streaming

### Global flags

- `--json` — output as JSON (machine-readable)
- `--debug` — print raw server JSON alongside rendered output
- `--profile <name>` — use a specific config profile
- `--instance <url>` — override instance URL

## Interactive shell (`repl`)

```
chatto repl
chatto:myspace:#general > messages 30
chatto:myspace:#general > send hello from the repl
chatto:myspace:#general > watch
chatto:myspace:#general > unwatch
chatto:myspace:#general > exit
```

Within the REPL, when a space and room are set with `use`, typing anything not recognized as a command sends it as a message.

## Configuration

Sessions are stored in `~/.config/chatto/config.toml`:

```toml
default_profile = "default"

[profiles.default]
instance = "https://chat.example.com"
session  = "..."
login    = "you"

[profiles.work]
instance = "https://chat.work.example.com"
session  = "..."
```

Run `chatto login --profile work` to add a second profile.

## Output format

Messages are rendered in an IRC-like format:

```
[20:15] [#general] <Alice> hey everyone
[20:16] [#general] [Thread "hey everyone"] <Bob> welcome back
         ↩ Alice: "hey everyone"
```

Thread context is fetched on demand and cached. Video attachments are rendered using their processed URL once transcoding completes.

## License

AGPL-3.0 — see [LICENSE](LICENSE).
