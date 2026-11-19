# thingsctl

A command-line tool and MCP server that lets AI agents work with [Things 3](https://culturedcode.com/things/) on macOS. It is designed to cost agents as few tokens as possible and to use as little CPU and memory as possible.

> **Unofficial.** thingsctl is not affiliated with or endorsed by Cultured Code. "Things" is a trademark of Cultured Code GmbH & Co. KG.

**Status:** reading, writing and the MCP server work; see [Releases](https://github.com/Frozelo/thingsctl/releases) for versions.

## Design

- **Read-only database access.** thingsctl reads the local Things database with `mode=ro` and `query_only`, and never writes to it. Every change goes through Things itself, using AppleScript or the URL scheme, so Things Cloud sync stays safe.
- **Compact output.** Each item takes one line. Short ids are the shortest prefix that is still unique. Empty fields are left out. Dates are absolute, and today's date is printed once in the header.
- **Small and fast.** thingsctl is a single static Go binary with no cgo and no background process. It uses about 8 MB of RSS per call.

## Usage

```sh
thingsctl read                 # counts for every list
thingsctl read today           # Today, grouped by project, with an evening section
thingsctl read upcoming        # scheduled items and the next repeat of each repeating item
thingsctl read -q milk         # search titles and notes
thingsctl read --tag work      # open items that have a tag
thingsctl read --id a3f9       # one item in full: notes, checklist, dates
thingsctl read help            # output format and every flag
```

Writing:

```sh
thingsctl add "Buy milk" --when tomorrow --tags home    # prints: ok <id> Buy milk @11-20 #home
thingsctl add "Pack" --in Trip --checklist "socks; charger"
thingsctl add "Q4 plan" --type project --in Work --items "draft; review"
thingsctl update a3f9 --status done                     # also: canceled, open
thingsctl update "Buy milk" --when someday              # an exact title works as an id
thingsctl update a3f9 --when evening --notes "call first" --notes-mode append
thingsctl update a3f9 --tags urgent --tags-mode add     # tag modes: set, add, remove
thingsctl trash a3f9                                    # moves to the Trash
thingsctl show today                                    # opens the list in Things
thingsctl batch - < items.json                          # Things JSON, for bulk changes
```

Items can be named by id or by exact title. An ambiguous title is an error that lists the matching ids. thingsctl confirms every write by reading the database back. If Things has not recorded the change within 1.5 s, it reports `sent … unconfirmed` instead of `ok`. The `ok` line shows the item as Things saved it, including its list and parent, for example `ok a3f9 Buy milk someday in:"Home"`, so agents do not need to read it again. Add `--dry-run` to see the exact AppleScript or URL without changing anything. Set `THINGSCTL_READONLY=1` to turn off writing entirely.

How thingsctl sends writes to Things:

- **AppleScript** is the default. It needs no token, it does not bring Things to the front, and it returns the new item's id. User text is passed to the script as arguments and is never inserted into the script source.
- **The Things URL scheme** is used only for what AppleScript cannot do: evening, reminders (`--when today@18:00`), headings, checklists and `batch`. Editing an existing item this way needs Things → Settings → General → Enable Things URLs.
- **Things itself blocks some edits.** You cannot change the when date or deadline of a repeating to-do, and repeating templates are read-only. Headings can only be created with `add --heading`. Checklist items are replaced as a whole list.
- **Permanent deletes ask for `--confirm`.** This applies to `trash --empty` and to deleting areas and tags.

Example read output:

```
today=11-19 today:3
a3f9 Pay rent !11-20 #home
Work b2c4:
 c9d1 Draft report #urgent [1/3]
evening:
 d0e1 Call mom @18:00
```

## MCP server

`thingsctl mcp` runs an MCP server on stdio with six tools: `read`, `add`, `update`, `trash`, `batch` and `show`. They take the same arguments as the CLI verbs, with `-` in flag names written as `_` (`notes_mode`). The whole tool list is about 3 KB of JSON, and tool results use the same compact text as the CLI.

Claude Code:

```sh
claude mcp add things -- thingsctl mcp
```

Codex (`~/.codex/config.toml`):

```toml
[mcp_servers.things]
command = "thingsctl"
args = ["mcp"]
```

Cursor, Claude Desktop and other clients:

```json
{"mcpServers": {"things": {"command": "/full/path/to/thingsctl", "args": ["mcp"]}}}
```

Server flags:

- `--read-only` exposes only the `read` tool. `THINGSCTL_READONLY=1` does the same.
- `--allow-permanent` lets `trash` empty the Trash and delete areas and tags. Without it, the agent can only move to-dos and projects to the Trash.

The server is a process of the client app, so that app needs Full Disk Access to read the Things database.

## Install

With Homebrew:

```sh
brew install frozelo/tap/thingsctl
```

Or with Go:

```sh
go install github.com/Frozelo/thingsctl/cmd/thingsctl@latest
```

Prebuilt binaries for Apple silicon and Intel Macs are on the [Releases](https://github.com/Frozelo/thingsctl/releases) page. They are not signed or notarized by Apple. The Homebrew cask removes the macOS quarantine flag on install. If you download a binary by hand, run `xattr -d com.apple.quarantine thingsctl` before the first start.

thingsctl needs macOS with Things 3 installed and opened at least once. Check the setup with:

```sh
thingsctl doctor
```

```
thingsctl 0.1.0 darwin/arm64
ok   database: ~/Library/Group Containers/…/main.sqlite (version 29)
ok   schema: compatible
warn things urls: off. Only needed to change evening, reminders, headings or checklists of existing items: …
ok   automation: allowed (Things 3.24.1)
```

`doctor` changes nothing and never launches Things. It exits with code 1 if a check fails. Two macOS permissions matter, and both belong to the app that runs thingsctl, such as Terminal, Claude or Cursor:

- **Full Disk Access** lets thingsctl read the Things database. Grant it in System Settings → Privacy & Security → Full Disk Access, then restart the app.
- **Automation → Things3** lets thingsctl send changes to Things. macOS asks for it on the first write.

## License

[MIT](LICENSE)
