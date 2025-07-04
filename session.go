package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type Session struct {
	ptmx *os.File
}

func NewSession() (*Session, error) {
	cmd := exec.Command("/bin/bash")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("error starting pty: %w", err)
	}

	return &Session{ptmx: ptmx}, nil
}

func (s *Session) Start(socket net.Conn) {
	go io.Copy(socket, s.ptmx)
	go io.Copy(s.ptmx, socket)
}

func (s *Session) Close() {
	s.ptmx.Close()
}
