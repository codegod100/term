package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gdamore/tcell/v2"
	"github.com/hinshun/vt10x"
)

// PaneBuffer holds the content for a single pane using vt10x terminal emulator
type PaneBuffer struct {
	terminal vt10x.Terminal
	width    int
	height   int
}

func NewPaneBuffer(width, height int) *PaneBuffer {
	term := vt10x.New(vt10x.WithSize(width, height))
	
	return &PaneBuffer{
		terminal: term,
		width:    width,
		height:   height,
	}
}

func (pb *PaneBuffer) Write(p []byte) (n int, err error) {
	// Let vt10x handle all the ANSI parsing
	return pb.terminal.Write(p)
}

func (pb *PaneBuffer) GetContent() [][]rune {
	// Get terminal content from vt10x
	pb.terminal.Lock()
	defer pb.terminal.Unlock()
	
	content := make([][]rune, pb.height)
	
	for y := 0; y < pb.height; y++ {
		content[y] = make([]rune, pb.width)
		for x := 0; x < pb.width; x++ {
			cell := pb.terminal.Cell(x, y)
			content[y][x] = cell.Char
		}
	}
	return content
}

func (pb *PaneBuffer) GetCursor() (int, int) {
	pb.terminal.Lock()
	defer pb.terminal.Unlock()
	
	cursor := pb.terminal.Cursor()
	return cursor.X, cursor.Y
}

func runClient() {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		cmd := exec.Command(os.Args[0], "daemon")
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Error starting daemon: %s\n", err)
			os.Exit(1)
		}
		for i := 0; i < 20; i++ {
			conn, err = net.Dial("unix", socketPath)
			if err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error connecting to daemon after starting it: %s\n", err)
			os.Exit(1)
		}
	}
	defer conn.Close()

	screen, err := tcell.NewScreen()
	if err != nil {
		panic(err)
	}
	if err = screen.Init(); err != nil {
		panic(err)
	}
	defer screen.Fini()

	// Initialize UI and client state
	ui := NewUI(screen)
	screen.SetStyle(ui.defStyle)
	clientState := NewClientState(ui)
	

	chWinSize := make(chan os.Signal, 1)
	signal.Notify(chWinSize, syscall.SIGWINCH)
	go func() {
		for range chWinSize {
			screen.Sync()
			width, height := screen.Size()
			clientState.UpdatePaneBufferSizes()
			ws := pty.Winsize{Rows: uint16(height - 1), Cols: uint16(width)} // -1 for status line
			payload, err := json.Marshal(ws)
			if err != nil {
				continue
			}

			header := make([]byte, 5)
			header[0] = 0x01 // resize
			binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
			msg := append(header, payload...)
			conn.Write(msg)
		}
	}()
	chWinSize <- syscall.SIGWINCH // Initial resize

	// Goroutine to handle incoming messages from the daemon
	go func() {
		for {
			header := make([]byte, 5)
			_, err := io.ReadFull(conn, header)
			if err != nil {
				return
			}

			msgType := header[0]
			payloadLen := binary.BigEndian.Uint32(header[1:])
			payload := make([]byte, payloadLen)
			_, err = io.ReadFull(conn, payload)
			if err != nil {
				return
			}

			// Debug: log all received messages
			if f, err := os.OpenFile("/tmp/term-client.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				f.WriteString(fmt.Sprintf("Client: Received message type 0x%02x, payload length %d\n", msgType, len(payload)))
				f.Close()
			}
			
			switch msgType {
			case 0x00: // data
				clientState.HandleDataMessage(payload)
			case 0x01: // resize (client sends, daemon processes, not expected here)
			case 0x08: // redraw (daemon sends to client)
				clientState.HandleRedrawMessage(payload)
			case 0x0A: // new pane notification (daemon sends to client)
				clientState.HandleNewPaneMessage(payload)
			case 0x0B: // switch pane notification
				clientState.HandleSwitchPaneMessage(payload)
			}
		}
	}()

	// Input handling loop using tcell
	prefixMode := false
	for {
		event := screen.PollEvent()
		switch ev := event.(type) {
		case *tcell.EventResize:
			chWinSize <- syscall.SIGWINCH // Trigger resize handler
		case *tcell.EventKey:
			key := ev.Key()
			runeChar := ev.Rune()

			var inputData []byte
			if key == tcell.KeyCtrlA {
				prefixMode = true
				continue
			} else if prefixMode {
				prefixMode = false
				switch runeChar {
				case 'd':
					return // Detach
				case 'c':
					inputData = []byte{0x02} // new window
				case 'n':
					inputData = []byte{0x03} // next window
				case 'p':
					inputData = []byte{0x04} // prev window
				case '&':
					inputData = []byte{0x05} // kill window
				case '"':
					inputData = []byte{0x06} // split horizontal
				case 'o':
					inputData = []byte{0x07} // next pane
				case '?':
					inputData = []byte{0x09} // show help
				case prefixKey:
					inputData = []byte{byte(prefixKey)} // pass through Ctrl+a
				default:
					// Unrecognized command, send prefix and the command character
					header := make([]byte, 5)
					header[0] = 0x00 // data
					binary.BigEndian.PutUint32(header[1:], 1)
					msg := append(header, prefixKey)
					conn.Write(msg)
					inputData = []byte{byte(runeChar)}
				}
			} else {
				if runeChar != 0 {
					inputData = []byte(string(runeChar))
				} else {
					switch key {
					case tcell.KeyEnter:
						inputData = []byte{13}
					case tcell.KeyBackspace, tcell.KeyBackspace2:
						inputData = []byte{0x7f} // ASCII DEL for backspace
					case tcell.KeyTab:
						inputData = []byte{9}
					case tcell.KeyCtrlC:
						inputData = []byte{0x03} // EOT
					case tcell.KeyCtrlD:
						inputData = []byte{0x04} // EOT
					case tcell.KeyCtrlL:
						inputData = []byte{0x0c} // Form Feed (clear screen)
					default:
						continue
					}
				}
			}

			if len(inputData) > 0 {
				header := make([]byte, 5)
				// Check if this is a command byte or actual data
				if len(inputData) == 1 && inputData[0] >= 0x02 && inputData[0] <= 0x09 {
					// This is a command, send as command message type
					header[0] = inputData[0]
					binary.BigEndian.PutUint32(header[1:], 0) // No payload for most commands
					msg := header
					
					
					conn.Write(msg)
				} else {
					// This is regular data
					header[0] = 0x00 // data
					binary.BigEndian.PutUint32(header[1:], uint32(len(inputData)))
					msg := append(header, inputData...)
					conn.Write(msg)
				}
			}
		}
	}
}

