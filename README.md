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
