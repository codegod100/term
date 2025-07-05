# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a terminal multiplexer implementation in Go that allows multiple terminal sessions to be managed through a single interface. The architecture follows a client-daemon model where a daemon manages terminal sessions and multiple clients can connect to interact with them.

## Build and Run Commands

```bash
# Build the project
go build

# Run as client (default mode)
./term

# Run daemon directly (usually not needed as client auto-starts daemon)
./term daemon
```

## Architecture

### Core Components

**Daemon (`daemon.go`)**: Unix socket server at `/tmp/term.sock` that manages a single shared session. All clients connect to the same session, enabling true multiplexing.

**Session Management (`session.go`)**: 
- `Session` manages multiple panes within a single session
- `SessionManager` handles client connections and message routing
- Uses binary protocol with message types (0x00=data, 0x06=split, 0x0A=new pane, etc.)
- Thread-safe with mutex protection for concurrent client access

**Pane Management (`pane.go`)**: Each pane wraps a `/bin/zsh` process with a PTY. Uses `TERM=xterm-256color` for full terminal feature support.

**Client (`client.go`)**: 
- TUI using tcell for terminal interface
- Uses vt10x library for proper ANSI escape sequence parsing
- `PaneBuffer` wraps vt10x terminal emulator for accurate terminal state
- Supports tmux-style key bindings with Ctrl+a prefix

**Modular UI (`ui.go`, `clientstate.go`)**:
- `UI` handles tcell screen drawing operations  
- `ClientState` manages pane buffers and application state
- Separation allows independent testing of UI vs business logic

### Message Protocol

Binary protocol over Unix socket:
- Header: 5 bytes (1 byte type + 4 bytes payload length)
- Data messages (0x00): Include 4-byte pane ID prefix + terminal data
- Command messages (0x02-0x09): Direct command type as message type
- State sync messages (0x0A, 0x0B): JSON payloads for pane management

### Key Bindings

- `Ctrl+a d`: Detach from session
- `Ctrl+a c`: Create new pane  
- `Ctrl+a "`: Split horizontal (create new pane)
- `Ctrl+a n`: Next pane
- `Ctrl+a p`: Previous pane
- `Ctrl+a o`: Next pane (alias)
- `Ctrl+a &`: Kill current pane
- `Ctrl+a ?`: Show help

### Dependencies

- `github.com/creack/pty`: PTY management for terminal processes
- `github.com/gdamore/tcell/v2`: Terminal UI framework
- `github.com/hinshun/vt10x`: VT100/xterm terminal emulator for ANSI parsing

## Development Notes

### Threading Model
The daemon uses goroutines extensively:
- One goroutine per pane for reading PTY output
- One goroutine per client connection for message handling
- Shared session state protected by mutexes

### Client Synchronization
New clients connecting to existing sessions will see current state through redraw mechanisms. All clients share the same session and see the same pane content.

### ANSI Handling
The project uses vt10x for proper terminal emulation instead of custom ANSI parsing. This enables full support for modern terminal features like 24-bit color, bracket paste mode, and complex cursor positioning.