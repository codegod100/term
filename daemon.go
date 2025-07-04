package main

import (
	"fmt"
	"net"
	"os"
)

const socketPath = "/tmp/term.sock"

func runDaemon() {
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listening on socket: %s\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error accepting connection: %s\n", err)
			continue
		}

		session, err := NewSession()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating session: %s\n", err)
			conn.Close()
			continue
		}
		defer session.Close()

		session.Start(conn)
	}
}
