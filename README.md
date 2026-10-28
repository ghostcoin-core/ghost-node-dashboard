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
