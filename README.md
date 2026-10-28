# Ghost Node Dashboard

A small program that runs next to a Ghost node and shows it in your browser, on your own computer only.

- See your node: whether it is running and in sync, its peers, and the network it is on.
- See your mining: how many cores it uses, the blocks it has won, and what it has earned.
- Control it: choose how many cores to mine on, pause and resume mining, change where rewards go, and update or restart the node.

The dashboard listens on 127.0.0.1 only and talks to the node through the node's own local connection. Nothing is reachable from other computers.

It is installed with the node by the one-line install at https://get.ghostcoin.com.

## Running it

```
ghost-dash                 # serve the dashboard on http://127.0.0.1:8580
ghost-dash open            # print the sign-in link and open it in your browser
```

Flags (for both):

- `--node-dir` — the node's directory. Default `~/.ghost-node` (`%USERPROFILE%\.ghost-node` on Windows).
- `--port` — the port on 127.0.0.1. Default 8580, or `GHOST_DASH_PORT` when set.

The dashboard reads the node through its IPC endpoint: `<node-dir>/data/geth.ipc` on macOS and Linux, the named pipe `\\.\pipe\geth.ipc` on Windows. When the node is stopped the page says so and picks the node up again when it starts.

### What the page shows

The page refreshes every 5 seconds without reloading. Each panel says "Node not running" while the node is down.

- **Your node:** running or not; in sync, or syncing with the current and highest block; peers and the clients they run; client version; how long the node has been up (from when it opened its IPC socket, or, on Windows, from the dashboard's first answer after it came up); the network ("Ghost test network" for chain ID 6777972); whether automatic updates are installed (`~/Library/LaunchAgents/com.ghostcoin.node.update.plist` on macOS, the `ghost-node-update.timer` systemd user timer on Linux, the `GhostNodeUpdate` scheduled task on Windows).
- **Mining:** mining or paused; cores used (`<node-dir>/miner-threads`, else every core) of the computer's cores; the node's hash rate when it reports one; the payout address with a copy button; and estimates of your share of the network and the time to your next block.
- **Network:** height; average block time over the last 100 blocks; difficulty; an estimated network hash rate (difficulty ÷ the target block time); and the last 20 blocks, with your own in violet.

- **Rewards:** the blocks your payout address won (number, time and reward), how many you won and what they earned today (this computer's time) and in total, and the payout address balance. A block's reward is the payout address's balance change across that block, read from the node; when the node no longer keeps the balances of an old block its reward shows as unknown and is left out of the sums. The panel also counts the node log's "block lost" lines: each one is a lost race, where your node found a block but another node's block at the same height was accepted first. That is normal and earns nothing.
- **Node log:** the last 200 lines of `<node-dir>/node.log`, newest first, with a plain meaning beside common lines (mined potential block, block lost, imported new chain segment, looking for peers). The log is only read, and only from inside the node folder: a `node.log` that links elsewhere is refused.

The dashboard finds your blocks by reading the chain over IPC and keeps what it found in `<node-dir>/dashboard-index.json` (owner-only), with the last block it read, so after a restart it only reads new blocks. On a long chain the first reading is spread over several refreshes. When the chain is reorganised it drops the blocks the new chain replaced and reads again from there; when the payout address changes it starts over.

### Mining controls

The Mining panel has a cores slider, from 1 to the computer's logical core count, and a pause or resume button.

- **Apply** asks the node to mine on that many cores at once (`miner_start`) and saves the number in `<node-dir>/miner-threads`, the file the installer and updater read, so restarts and updates keep it. The file is replaced in one step (a temporary file renamed over it) and keeps its permissions. While mining is paused the number is only saved, and resuming uses it.
- **Pause** stops mining (`miner_stop`) and writes `<node-dir>/mining-paused`; the node keeps syncing. **Resume** removes that file and starts mining on the saved core count. Until the node installer reads `mining-paused`, a node restart resumes mining.

After each change the dashboard reads the node back (`eth_mining`) and says in plain words what happened, or what went wrong. The controls are `POST /api/mining/cores` (body `{"cores": n}`, a whole number in range, else 400), `POST /api/mining/pause` and `POST /api/mining/resume`, behind the dashboard key and the Origin check.

### Payout address

The Mining panel has a payout address field. It takes `0x` and 40 hex characters; an address in mixed case must match its checksum (EIP-55), and one all in lower or upper case is taken as is. Saving asks the node to pay to it at once (`miner_setEtherbase`) and rewrites the `Etherbase = "…"` line of `<node-dir>/config.toml`, so restarts keep it. The file is replaced in one step, keeps its permissions, and no other line changes (when the line is missing it is added under `[Eth.Miner]`, as the installer does). A missing `config.toml` is not created. The new address shows on the Mining panel, and the Rewards panel starts counting blocks for it. The control is `POST /api/mining/payout` (body `{"address": "0x…"}`, else 400). Only the address is needed: the dashboard never asks for a private key or recovery words.

### Restart and updates

The Restart and updates panel restarts the node, shows whether daily updates are on and when they last ran, runs an update at once and turns daily updates on or off. The dashboard reads the service names from the files the installer wrote: the launchd agent in `~/Library/LaunchAgents` (macOS) or the systemd user unit in `${XDG_CONFIG_HOME:-~/.config}/systemd/user` (Linux) whose program is `<node-dir>/bin/geth` is the node, and the one whose program is `<node-dir>/update` is the updater (with the installer, `com.ghostcoin.node` and `com.ghostcoin.node.update`, or `ghost-node.service` and `ghost-node-update.timer`).

| Action | macOS | Linux |
| --- | --- | --- |
| Restart node | `launchctl kickstart -k gui/<uid>/<node label>` | `systemctl --user restart <node unit>` |
| Updates on | `launchctl bootstrap gui/<uid> <update plist>` | `systemctl --user enable --now <update timer>` |
| Updates off | `launchctl bootout gui/<uid>/<update label>` (the plist is kept) | `systemctl --user disable --now <update timer>` |
| Update now | `<node-dir>/update` | `<node-dir>/update` |
| On or off | `launchctl print gui/<uid>/<update label>` | `systemctl --user is-enabled <update timer>` |
| Last run | time of the agent's log (`<node-dir>/update.log`) | the timer's `LastTriggerUSec` |

After a restart the dashboard waits up to a minute for the node to answer again and says so. "Update now" shows the updater's output (the last 64 KiB). Commands run with fixed arguments, never through a shell, and one restart or update runs at a time. On macOS the update agent stays in `~/Library/LaunchAgents`, so launchd loads it again at the next login, and the updater itself may turn daily updates back on when it reinstalls the node; the panel always shows the state read back. On Windows the node starts from its login entry (`geth service start`), not a scheduled task, so these controls say they are not available there yet.

The controls are `POST /api/node/restart`, `POST /api/updates/run`, `POST /api/updates/on` and `POST /api/updates/off`, and the state is `GET /api/updates`, all behind the dashboard key and the Origin check.

The estimates use a target block time of 15 seconds. Each panel reads one endpoint, behind the dashboard key: `/api/node`, `/api/mining`, `/api/network`, `/api/rewards` and `/api/log`.

### Access

The dashboard key lives in `<node-dir>/dashboard-key` (created with owner-only permissions if it is missing). Opening `http://127.0.0.1:<port>/open?key=<key>` signs the browser in with a cookie; every other request without that cookie is refused. The dashboard also refuses requests addressed to any host other than `127.0.0.1:<port>` or `localhost:<port>`, sends no CORS headers, and only accepts POSTs from its own page.

## Building

Go only, no third-party modules:

```
go build ./cmd/ghost-dash
bash scripts/ci-gate.sh    # vet and test
```

## Licence

Licensed under either of the Apache License, Version 2.0 (LICENSE-APACHE) or the MIT licence (LICENSE-MIT), at your option.
