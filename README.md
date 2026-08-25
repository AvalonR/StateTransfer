# StateTransfer

Cross-platform, zero-knowledge state transfer between your own devices: push text, links, and files securely over TCP with end-to-end encryption (NaCl `box`), discovered via mDNS.

**Status:** Go daemon networking engine is done; the Tauri desktop shell (Rust bridge + Svelte frontend) is the active work item.

## Quick start

```bash
# daemon (headless)
go run . --port=9092

# two-instance local test (second terminal)
go run . --port=9093 --identity=/tmp/id2 --peer=127.0.0.1:9092

# Tauri desktop shell
cd tauri && pnpm install && pnpm tauri dev
```

See [docs/06-testing.md](docs/06-testing.md) for the full guide.

## License

Licensed under the **Prosperity Public License 3.0.0** — free for noncommercial use; commercial use requires a license (see [LICENSE](LICENSE)).
