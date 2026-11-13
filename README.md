# Mountkeeper

A native macOS SMB share manager for the terminal.

Mountkeeper remembers your network shares and connects, disconnects and opens
them with one command — instead of clicking through Finder's *Connect to
Server* dialog or remembering mount commands. Passwords live in the macOS
Keychain and nowhere else.

```
  Mountkeeper

  Available network shares:

  ❯ ● projects      Connected
    ○ development   Disconnected
    ○ media         Disconnected

  ↑/↓ Navigate   Enter Disconnect   O Open in Finder   R Refresh   Q Quit
```

macOS only, by design: it uses the system's own SMB client, Keychain,
launchd and Finder rather than reimplementing any of them.

## Install

```sh
brew tap sanriodev/mountkeeper https://github.com/sanriodev/mountkeeper
brew install mountkeeper
```

The explicit URL is needed because the tap lives in the application
repository itself (Homebrew would otherwise look for `homebrew-mountkeeper`).
Homebrew compiles Mountkeeper from the tagged release source; no prebuilt
binaries are downloaded.

From a checkout (needs Go and the Xcode Command Line Tools):

```sh
make build      # → bin/mountkeeper
make install    # → /usr/local/bin/mountkeeper
```

## Usage

```sh
mountkeeper add smb://fileserver.example.com/projects   # register (asks for the details)
mountkeeper connect                                     # interactive manager
mountkeeper connect projects --open                     # connect and show in Finder
mountkeeper open projects                               # connect if needed, then open Finder
mountkeeper disconnect projects
mountkeeper disconnect --all
```

| Command | What it does |
| --- | --- |
| `add <smb-url>` | Register a share. Asks for name, user, Keychain, mount point, Finder and auto-connect; `--yes` takes the defaults, flags answer individual questions. |
| `remove <share>` | Unregister a share and delete its Keychain item. |
| `list` | Registered shares with URL, mount point and options. |
| `status` | Live connection state of every share. |
| `info <share>` | Everything about one share. |
| `connect [share...]` | Connect by name, `--all`, or interactively without arguments. `--open` / `--no-open` control Finder. |
| `disconnect [share...]` | Disconnect by name or `--all`. `--force` unmounts even with open files. |
| `open <share>` | Open the share in Finder, connecting first if necessary. |
| `autoconnect [share] [true\|false]` | Connect a share automatically at login. Without arguments: show the settings. |
| `find [host]` | Without a host: SMB servers announced on the local network. With a host: the shares it offers. |
| `credentials set\|remove <share>` | Replace or delete the stored password. |

Shell completion (including share names) is installed by Homebrew; otherwise
see `mountkeeper completion --help`.

### How state is detected

Mountkeeper keeps no record of what is connected. Status always comes from
the kernel mount table (`getfsstat(2)`, the data behind `mount`), so a share
you connected through Finder shows up as connected too — reported as
*Connected at /Volumes/…* when it is mounted somewhere other than its
configured mount point.

### Mount points

Shares are mounted under `~/Network/<name>` unless you choose another path.
Mountkeeper creates the directory when needed and refuses to mount over a
file, a non-empty directory, or a directory another volume is mounted on.
After disconnecting, the empty directory is removed again.

### Automatic connecting

`mountkeeper autoconnect <share> true` installs a per-user LaunchAgent
(`~/Library/LaunchAgents/com.github.sanriodev.mountkeeper.autoconnect.plist`)
that runs `mountkeeper connect --auto` at login and every five minutes, which
also restores shares after sleep or a network change. It is removed as soon
as no share uses it. There is no daemon.

The agent never prompts. It skips servers that are unreachable, and if a
server rejects the stored password it **pauses that share** instead of
retrying — repeated failed logins could lock your account. Connecting
manually or running `mountkeeper credentials set <share>` resumes it. Its log
is `~/Library/Logs/mountkeeper.log`.

## Security

- Passwords are stored only in the login Keychain, as generic passwords with
  service `mountkeeper` and the share id as account. You can inspect them in
  *Keychain Access*.
- Passwords are never written to the configuration file, never logged, and
  never passed on a command line. A URL containing a password is rejected.
- Mounting goes through the system's NetFS framework — the same API Finder
  uses — which takes the password in memory. `mount_smbfs` is deliberately not
  used for this: it only accepts a password as part of its argument, where
  any local process could read it from the process list.
- All external tools (`diskutil`, `open`, `launchctl`, `dns-sd`, `smbutil`)
  are run directly with an argument list, never through a shell.

Mountkeeper is not code-signed with a developer identity. macOS ties Keychain
access to the binary, so after an upgrade it may ask once whether the new
`mountkeeper` may use the stored password; choose *Always Allow*.

## Configuration

`~/.config/mountkeeper/config.yaml` (override with `MOUNTKEEPER_CONFIG`):

```yaml
version: 1
shares:
  - id: projects
    name: projects
    protocol: smb
    host: fileserver.example.com
    path: /projects
    domain: CORP            # optional
    username: alice         # omit for guest access
    mount_point: ~/Network/projects
    keychain_account: projects
    auto_open: true
    auto_connect: false
```

`keychain_account` names the Keychain item holding the password; leave it out
to be asked on every connect instead.

## Development

```sh
make check   # gofmt, go vet, tests, build — what CI runs
make test
MOUNTKEEPER_KEYCHAIN_TEST=1 go test ./internal/keychain/   # round trip against your login Keychain
```

```
cmd/mountkeeper/     entry point
internal/cli/        commands, prompts, output
internal/tui/        interactive manager (Bubble Tea)
internal/share/      share model, SMB URL parsing
internal/config/     YAML configuration
internal/keychain/   Security framework (cgo)
internal/mount/      mount table, NetFS mounting (cgo), unmounting
internal/finder/     Finder
internal/macos/      launchd agent, Bonjour / smbutil discovery
Formula/             Homebrew formula (this repository is its own tap)
```

### Releasing

The repository must be public: Homebrew downloads the release's source
archive without authentication.

Push a version tag:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

The release workflow tests and builds the tagged commit, computes the SHA-256
of the tag's source archive, rewrites `url` and `sha256` in
`Formula/mountkeeper.rb`, installs that formula from source and runs
`brew test`. Only if all of that succeeds does it publish: first the GitHub
release, then the formula update on `main`.

Every step can be repeated. If a release fails, fix the cause and run the
workflow again for the same tag: *Actions → Release → Run workflow*, enter the
tag. Nothing needs to be deleted first.

## License

MIT
