# Use Cage with an external Agent

Start with [the complete Agent guide](docs/guide/agent.md). It covers locating the installed CLI, release download and checksum verification, platform installation, accounts, proxy setup, guest installation, settings, job tracking and recovery.

Run `<installed Cage CLI> agent capabilities` to discover the protocol and supported input fields. Every `agent` command returns JSON. The source guide may describe functionality absent from a published release; detect support before configuring it.

The Agent uses the same configuration, account checks, native operations and client preferences as the user interface. Use `agent routes` for the live HTTP route list, `agent request METHOD /api/PATH` for JSON or file operations, and `agent socket /api/PATH` for WebSocket NDJSON. Desktop configuration and selected workloads require an explicit scope and connection generation as described in the guide. Native credential operations stay inside the existing OS bridge.

Website entry points: [agent.md](docs/guide/agent.md), [llms.txt](www/public/llms.txt), [download helper](https://raw.githubusercontent.com/tan396222-prog/Claude-AntiBan/main/www/public/agent-bootstrap.mjs).

`AGENTS.md` contains repository contributor instructions. Product users and their agents should follow the usage guide above.
