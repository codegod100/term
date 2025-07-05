package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type Pane struct {
	ptmx   *os.File
	output chan []byte
	id     int
}

func NewPane(id int) (*Pane, error) {
	cmd := exec.Command("/bin/bash")
	// Set environment to disable colored prompts
	cmd.Env = append(os.Environ(), "TERM=dumb", "PS1=$ ")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("error starting pty: %w", err)
	}

	return &Pane{
		ptmx:   ptmx,
		output: make(chan []byte, 1024),
		id:     id,
	}, nil
}

func (p *Pane) Start() {
	// Send clear screen command to the new shell
	p.ptmx.Write([]byte("clear\n"))
	
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := p.ptmx.Read(buf)
			if err != nil {
				close(p.output)
				return
			}
			p.output <- buf[:n]
		}
	}()
}

func (p *Pane) Close() {
	p.ptmx.Close()
}
