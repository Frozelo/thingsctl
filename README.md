# thingsctl

A command-line tool, and later an MCP server, that lets AI agents work with [Things 3](https://culturedcode.com/things/) on macOS. It is designed to cost agents as few tokens as possible and to use as little CPU and memory as possible.

> **Unofficial.** thingsctl is not affiliated with or endorsed by Cultured Code. "Things" is a trademark of Cultured Code GmbH & Co. KG.

**Status: early development.** Reading works. Writing and the MCP server are in progress.

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

Example output:

```
today=11-19 today:3
a3f9 Pay rent !11-20 #home
Work b2c4:
 c9d1 Draft report #urgent [1/3]
evening:
 d0e1 Call mom @18:00
```

## Install

```sh
go install github.com/Frozelo/thingsctl/cmd/thingsctl@latest
```

thingsctl needs macOS with Things 3 installed and opened at least once. If macOS denies access to the Things database, grant Full Disk Access to the app that runs thingsctl, such as Terminal, Claude or Cursor.

## License

[MIT](LICENSE)
