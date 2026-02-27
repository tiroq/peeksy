# peeksy

A two-part Go GUI application for transferring files between machines using QR codes.

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

```sh
# Build sender
go build -o peeksy-sender ./cmd/sender

# Build receiver
go build -o peeksy-receiver ./cmd/receiver
```

## Usage

### Sender (`cmd/sender`)

1. Launch `peeksy-sender`.
2. Click **Select File…** and choose the file to transfer.
3. The first QR code is displayed immediately.
4. Click **Next** to advance to the next chunk, or **Prev** to go back.
5. Use **Resend Part…** to jump to any specific chunk number (e.g. when the receiver reports a missing part).
6. The sender also starts an HTTP API on port **8765** that the receiver can use to automatically advance to the next part.

![Peeksy Sender](https://github.com/user-attachments/assets/2c1f07f8-bcaf-4f4c-be08-a7cc5e5d041f)

### Receiver (`cmd/receiver`)

1. Launch `peeksy-receiver`.
2. Click **Browse…** to choose where the reassembled file will be saved.
3. Set the **Sender** address (default: `http://127.0.0.1:8765`).
4. Enter the screen **region** (X, Y, W, H) that contains the sender's QR code.
5. Click **Capture & Read QR** to scan one frame, or **Start Auto-Capture** to scan automatically every 2 seconds.
6. Progress is shown in real time (received parts, missing parts).
7. If a part is missing use **Request Resend** — the receiver tells the sender to display the requested part.
8. Once all parts are received click **Save File**.

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
│   ├── sender/main.go    – Sender GUI + HTTP server
│   └── receiver/main.go  – Receiver GUI + screen capture
├── internal/
│   ├── chunker/          – File splitting & assembly
│   └── protocol/         – QR payload encoding/decoding
```

## Running Tests

```sh
go test ./internal/...
```