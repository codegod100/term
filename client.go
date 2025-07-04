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

	"golang.org/x/term"
	"github.com/creack/pty"
)

func runClient() {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		// If we can't connect, it probably means the daemon isn't running.
		// So, let's start it.
		cmd := exec.Command(os.Args[0], "daemon")
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Error starting daemon: %s\n", err)
			os.Exit(1)
		}
		// Wait for the daemon to start
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

	if term.IsTerminal(int(os.Stdin.Fd())) {
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			panic(err)
		}
		defer term.Restore(int(os.Stdin.Fd()), oldState)

		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGWINCH)
		go func() {
			for range ch {
				ws, err := pty.GetsizeFull(os.Stdin.Fd())
				if err != nil {
					continue
				}
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
		ch <- syscall.SIGWINCH
	}

	go io.Copy(os.Stdout, conn)

	input := make(chan []byte)
	go func() {
		for {
			b := make([]byte, 1024)
			n, err := os.Stdin.Read(b)
			if err != nil {
				close(input)
				return
			}
			input <- b[:n]
		}
	}()

	prefixMode := false
	for data := range input {
		if prefixMode {
			if len(data) == 1 {
				switch data[0] {
				case 'd':
					return // Detach
				case prefixKey:
					// pass through
				default:
					// Unrecognized command, send prefix and the command character
					header := make([]byte, 5)
					header[0] = 0x00 // data
					binary.BigEndian.PutUint32(header[1:], 1)
					msg := append(header, prefixKey)
					conn.Write(msg)
				}
			}
			prefixMode = false
		} else if len(data) == 1 && data[0] == prefixKey {
			prefixMode = true
			continue
		}

		header := make([]byte, 5)
		header[0] = 0x00 // data
		binary.BigEndian.PutUint32(header[1:], uint32(len(data)))
		msg := append(header, data...)
		conn.Write(msg)
	}
}
