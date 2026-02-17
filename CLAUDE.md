# camlink-fix

A Go daemon that automatically resets the Elgato Cam Link 4K when it becomes
unresponsive after macOS sleep/wake cycles.

## Architecture

- `cmd/camlink-fix/` - Entry point, event loop, signal handling
- `internal/usbwatch/` - IOKit USB device arrival detection via purego
- `internal/sleepwatch/` - Sleep/wake detection via mac-sleep-notifier
- `internal/health/` - Camera health checks (system_profiler + ffmpeg)
- `internal/reset/` - Escalating USB power cycle via uhubctl
- `internal/notify/` - macOS notifications via osascript
- `nix/` - Nix module and packaging

## Building

```bash
go build ./cmd/camlink-fix
```

## Running

```bash
./camlink-fix --uhubctl-path /path/to/uhubctl --ffmpeg-path /path/to/ffmpeg
```

## Nix

```bash
nix build
```

The flake exports `darwinModules.default` for use in nix-darwin configurations.
