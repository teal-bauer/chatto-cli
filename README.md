# chatto-cli

```sh
chatto rooms
chatto messages general
chatto send general "hello world"
chatto watch
```

Command-line client for [Chatto](https://chatto.run), a self-hosted team chat platform. Browse rooms, read and send messages, and stream live events from the terminal. Talks to the Chatto ConnectRPC API and the realtime protobuf WebSocket.

## Install

```sh
go install github.com/teal-bauer/chatto-cli@latest
```

Or build from source:

```sh
git clone https://github.com/teal-bauer/chatto-cli
cd chatto-cli
go build -o chatto .
```

`go install` produces a binary named `chatto-cli`. The examples below use `chatto`; rename or alias it.

## Quick start

```sh
chatto login                    # authenticate, save a profile
chatto rooms                    # list rooms on the server
chatto messages general         # recent messages in #general
chatto send general "hello"     # post a message
chatto watch                    # stream live events (Ctrl+C to stop)
```

Rooms are matched by ID, name, or `#name`.

## Commands

| Command | Description |
|---------|-------------|
| `login [profile]` | Authenticate and store a token |
| `logout [profile]` | Remove a stored profile |
| `profiles` | List saved profiles |
| `me` | Show the current user |
| `rooms [--all]` | List joined rooms, or all visible rooms with `--all` |
| `join <room>` | Join a room |
| `leave <room>` | Leave a room |
| `messages <room>` | Show recent messages |
| `send <room> <text…>` | Send a message |
| `watch` | Stream live events from the server |
| `repl` | Interactive shell |

### `messages` flags

- `-n N` / `--limit N`: number of messages to fetch (default 20)
- `--before <cursor>` / `--after <cursor>`: page through history using the opaque cursors printed by a previous page

### `watch` flags

- `--room <id|name>`: filter events to one room
- `--history N`: show the last N messages from `--room` before streaming (ignored with `--json`)

### Global flags

- `--json`: machine-readable JSON output
- `--debug`: print the raw protobuf JSON alongside rendered output
- `--profile <name>`: use a specific config profile
- `--instance <url>`: override the instance URL

## Interactive shell (`repl`)

```
chatto repl
chatto > use general
chatto:#general > messages 30
chatto:#general > send hello from the repl
chatto:#general > watch
chatto:#general > unwatch
chatto:#general > exit
```

`use <room>` sets the current room. Room names with spaces can be quoted: `use "off topic"`.

## Configuration

Profiles live in `~/.config/chatto/config.toml`:

```toml
default_profile = "default"

[profiles.default]
instance = "https://chat.example.com"
token    = "..."
login    = "you"
```

`chatto login --profile work` adds a second profile. The `token` is a bearer credential; keep it out of shared configs. `--instance`, `CHATTO_INSTANCE`, and `CHATTO_TOKEN` override the stored profile.

## Output

Messages render in an IRC-like format:

```
[20:15] [#general] <Alice> hey everyone
[20:16] [#general] [Thread "hey everyone"] <Bob> welcome back
         ↩ Alice: "hey everyone"
```

Reply and thread context is fetched on demand and cached. Video attachments render with their processed URL once transcoding finishes.

## License

[AGPL-3.0-or-later](LICENSE)
