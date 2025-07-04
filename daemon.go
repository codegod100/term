package main

import (
	"fmt"
	"net"
	"os"
	"sync"
)

const socketPath = "/tmp/term.sock"

type Daemon struct {
	listener net.Listener
	mainSession *Session // The single, shared session
	mutex    sync.Mutex
}

func NewDaemon() (*Daemon, error) {
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("error listening on socket: %w", err)
	}

	// Create the single main session when the daemon starts
	mainSession := NewSession("main-session") // Give it a fixed ID for now

	return &Daemon{
		listener: listener,
		mainSession: mainSession,
	}, nil
}

func (d *Daemon) Run() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			return
		}

		go func() {
			defer conn.Close()
			// All clients attach to the single main session
			sm := NewSessionManager(conn, d.mainSession)
			sm.Run() // This will block until the client disconnects or detaches
		}()
	}
}

func (d *Daemon) Close() {
	d.listener.Close()
	d.mainSession.Close() // Close the main session when the daemon exits
}

func runDaemon() {
	d, err := NewDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
	defer d.Close()

	d.Run()
}

