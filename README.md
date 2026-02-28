# peeksy

A Go GUI application for transferring files between machines using QR codes.
Sender and Receiver run as two tabs inside a single binary.

## Overview

**Sender** splits a file into base64-encoded chunks and displays each chunk as a QR code.  
**Receiver** captures the screen region containing the QR code, decodes each chunk, and reassembles the file.

Each QR code payload is a compact JSON object:

```json
{"c":2,"t":10,"d":"KJNiuefEF=="}
```

| Field | Meaning |
|-------|---------|
| `c`   | Current chunk index (1-based) |
| `t`   | Total number of chunks |
| `d`   | Base64-encoded bytes for this chunk |

## Building

### With Task (recommended)

```sh
# Build for current platform
task

# Cross-compile for all platforms → dist/
task build:all

# Build, tag v1.0.0, and publish a GitHub release
task tag -- v1.0.0
task release
```

### With plain Go

```sh
go build -o peeksy ./cmd/peeksy
```
## Usage

Launch `peeksy`. The window opens with two tabs at the top:

### Send tab

1. Click **Open File** and choose the file to transfer.
2. The first QR code is displayed immediately.
3. Click **Next** / **Prev** to navigate chunks.
4. Use **Resend Part…** to jump to any specific chunk (e.g. when the receiver reports a missing part).
5. The sender starts an HTTP API on port **8765** used by the Receive tab for auto-advance.

### Receive tab

1. Click **Browse…** to choose where the reassembled file will be saved.
2. Set the **Sender** address (default: `http://127.0.0.1:8765`).
3. Enter the screen **region** (X, Y, W, H) containing the sender's QR code.
4. Click **Capture QR** to scan one frame, or **Auto-Capture** to scan every 2 s.
5. Progress is shown in real time (received parts, missing parts).
6. Use **Request Resend** to ask the sender to re-display a missing part.
7. Once all parts are received click **Save File**.
## HTTP API (Sender)

The sender listens on `:8765` and accepts:

| Method | Path | Description |
|--------|------|-------------|
| `GET`  | `/api/info`        | Returns `{"total":N,"current":K}` |
| `POST` | `/api/next`        | Advance to next part |
| `POST` | `/api/prev`        | Go to previous part |
| `POST` | `/api/goto/{n}`    | Jump to part `n` |
| `GET`  | `/api/qr.png`      | Current QR as PNG |

## Project Structure

```
├── cmd/
│   ├── peeksy/main.go    – Unified binary (Send + Receive tabs)
│   ├── sender/main.go    – Standalone sender (legacy)
│   └── receiver/main.go  – Standalone receiver (legacy)
├── internal/
│   ├── chunker/          – File splitting & assembly
│   ├── protocol/         – QR payload encoding/decoding
│   └── ui/               – Shared theme & widgets
├── Taskfile.yml          – Build / release automation
```

## Running Tests

```sh
task test
# or
go test ./...
```