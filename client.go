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
)

// PaneBuffer holds the content for a single pane
type PaneBuffer struct {
	content [][]rune
	cursorX int
	cursorY int
	width   int
	height  int
	// ANSI parsing state
	inEscape bool
	escapeBuf []byte
	// For debugging ANSI parsing
	debugANSI bool
}

func NewPaneBuffer(width, height int) *PaneBuffer {
	content := make([][]rune, height)
	for i := range content {
		content[i] = make([]rune, width)
		for j := range content[i] {
			content[i][j] = ' '
		}
	}
	return &PaneBuffer{
		content: content,
		width:   width,
		height:  height,
		inEscape: false,
		escapeBuf: make([]byte, 0, 32),
		debugANSI: false, // Set to true for debugging ANSI parsing
	}
}

func (pb *PaneBuffer) Write(p []byte) (n int, err error) {
	for _, b := range p {
		if pb.debugANSI {
			// fmt.Printf("DEBUG_WRITE: Char: %q (0x%x), inEscape: %t, escapeBuf: %q\n", b, b, pb.inEscape, pb.escapeBuf)
		}

		if pb.inEscape {
			pb.escapeBuf = append(pb.escapeBuf, b)

			// Check for CSI sequence terminator (0x40-0x7E)
			if b >= 0x40 && b <= 0x7E {
				seq := string(pb.escapeBuf)
				// fmt.Printf("DEBUG_ANSI: CSI Sequence: %q\n", seq)
				// Process CSI sequence (e.g., cursor movement, clear screen)
				if len(seq) > 1 && seq[0] == '[' {
					switch seq[len(seq)-1] {
					case 'H': // Cursor Position: ESC[<ROW>;<COL>H
						// For simplicity, just move to 0,0 for now
						pb.cursorX = 0
						pb.cursorY = 0
					case 'J': // Erase in Display: ESC[2J (clear screen)
						for y := 0; y < pb.height; y++ {
							for x := 0; x < pb.width; x++ {
								pb.content[y][x] = ' '
							}
						}
						pb.cursorX = 0
						pb.cursorY = 0
					case 'm': // SGR (Select Graphic Rendition) - color/style
						// Consume, but don't apply style for now
					}
				}
				pb.inEscape = false
				pb.escapeBuf = pb.escapeBuf[:0]
			} else if b == '\a' { // BEL character, often terminates OSC
				// fmt.Printf("DEBUG_ANSI: OSC Sequence (terminated by BEL): %q\n", string(pb.escapeBuf))
				pb.inEscape = false
				pb.escapeBuf = pb.escapeBuf[:0]
			} else if len(pb.escapeBuf) > 1 && pb.escapeBuf[0] == ']' && b == '\\' { // ST (String Terminator) for OSC
				// fmt.Printf("DEBUG_ANSI: OSC Sequence (terminated by ST): %q\n", string(pb.escapeBuf))
				pb.inEscape = false
				pb.escapeBuf = pb.escapeBuf[:0]
			}
		} else if b == 0x1B { // ESC character
			pb.inEscape = true
			pb.escapeBuf = append(pb.escapeBuf, b)
		} else if b == '\n' {
			pb.cursorY++
			pb.cursorX = 0
		} else if b == '\r' {
			pb.cursorX = 0
		} else if b == 0x08 { // Backspace
			if pb.cursorX > 0 {
				pb.cursorX--
			}
			pb.content[pb.cursorY][pb.cursorX] = ' ' // Clear character at cursor
		} else {
			if pb.cursorX >= pb.width {
				pb.cursorX = 0
				pb.cursorY++
			}
			if pb.cursorY >= pb.height {
				// Simple scroll: shift all lines up, clear last line
				copy(pb.content, pb.content[1:])
				pb.content[pb.height-1] = make([]rune, pb.width)
				for i := 0; i < pb.width; i++ {
					pb.content[pb.height-1][i] = ' '
				}
				pb.cursorY = pb.height - 1
			}
			pb.content[pb.cursorY][pb.cursorX] = rune(b)
			pb.cursorX++
		}
	}
	return len(p), nil
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

