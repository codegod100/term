package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/creack/pty"
)

type Session struct {
	id          string
	panes       []*Pane
	activePane  int
	nextPaneID  int
	mutex       sync.Mutex
	clients     map[net.Conn]bool // Track connected clients
	clientMutex sync.Mutex
}

func NewSession(id string) *Session {
	s := &Session{
		id:      id,
		clients: make(map[net.Conn]bool),
	}
	s.NewPane() // Create an initial pane
	return s
}

func (s *Session) AddClient(conn net.Conn) {
	s.clientMutex.Lock()
	defer s.clientMutex.Unlock()
	s.clients[conn] = true
	fmt.Printf("Session %s: Client %v added. Total clients: %d\n", s.id, conn.RemoteAddr(), len(s.clients))
}

func (s *Session) RemoveClient(conn net.Conn) {
	s.clientMutex.Lock()
	defer s.clientMutex.Unlock()
	delete(s.clients, conn)
	fmt.Printf("Session %s: Client %v removed. Total clients: %d\n", s.id, conn.RemoteAddr(), len(s.clients))
}

func (s *Session) Broadcast(data []byte) {
	s.clientMutex.Lock()
	defer s.clientMutex.Unlock()
	fmt.Printf("Session %s: Broadcasting %d bytes to %d clients\n", s.id, len(data), len(s.clients))
	for conn := range s.clients {
		_, err := conn.Write(data)
		if err != nil {
			fmt.Printf("Session %s: Error writing to client %v: %v\n", s.id, conn.RemoteAddr(), err)
			// Consider removing client if write fails consistently
		}
	}
}

func (s *Session) NewPane() (*Pane, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	p, err := NewPane(s.nextPaneID)
	if err != nil {
		return nil, err
	}
	s.nextPaneID++
	p.Start()
	s.panes = append(s.panes, p)
	s.activePane = len(s.panes) - 1

	// Start a goroutine to read from the new pane and broadcast
	go func(pane *Pane) {
		for output := range pane.output {
			// Send data message
			header := make([]byte, 5)
			header[0] = 0x00 // data
			binary.BigEndian.PutUint32(header[1:], uint32(len(output)))
			msg := append(header, output...)
			s.Broadcast(msg)
		}
	}(p)

	s.redraw() // Redraw all clients after new pane
	return p, nil
}

func (s *Session) Close() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, p := range s.panes {
		p.Close()
	}
}

type SessionManager struct {
	conn    net.Conn
	session *Session
}

func NewSessionManager(conn net.Conn, session *Session) *SessionManager {
	return &SessionManager{conn: conn, session: session}
}

func (sm *SessionManager) Run() {
	sm.session.AddClient(sm.conn)
	defer sm.session.RemoveClient(sm.conn)

	// Initial redraw for the new client
	sm.session.redraw()

	for {
		header := make([]byte, 5)
		_, err := io.ReadFull(sm.conn, header)
		if err != nil {
			return
		}

		msgType := header[0]
		payloadLen := binary.BigEndian.Uint32(header[1:])
		payload := make([]byte, payloadLen)
		_, err = io.ReadFull(sm.conn, payload)
		if err != nil {
			return
		}

		sm.session.mutex.Lock()
		switch msgType {
		case 0x00: // data
			if len(sm.session.panes) > 0 {
				sm.session.panes[sm.session.activePane].ptmx.Write(payload)
			}
		case 0x01: // resize
			var ws pty.Winsize
			if err := json.Unmarshal(payload, &ws); err == nil {
				for _, p := range sm.session.panes {
					pty.Setsize(p.ptmx, &ws)
				}
			}
		case 0x02: // new window (now creates a new pane in the single session)
			sm.session.NewPane()
		case 0x03: // next window (now next pane)
			if len(sm.session.panes) > 0 {
				sm.session.activePane = (sm.session.activePane + 1) % len(sm.session.panes)
				sm.session.redraw()
			}
		case 0x04: // prev window (now prev pane)
			if len(sm.session.panes) > 0 {
				sm.session.activePane = (sm.session.activePane - 1 + len(sm.session.panes)) % len(sm.session.panes)
				sm.session.redraw()
			}
		case 0x05: // kill window (now kill pane)
			if len(sm.session.panes) > 0 {
				sm.session.RemovePane(sm.session.panes[sm.session.activePane].id)
			}
		case 0x06: // split horizontal (already handled by NewPane)
			sm.session.NewPane()
		case 0x07: // next pane (already handled by 0x03/0x04)
			// This case is now redundant with 0x03/0x04, but keeping for now.
			if len(sm.session.panes) > 0 {
				sm.session.activePane = (sm.session.activePane + 1) % len(sm.session.panes)
				sm.session.redraw()
			}
		case 0x09: // show help
			helpMsg := "Commands:\n"
			helpMsg += "  Ctrl+a d: Detach\n"
			helpMsg += "  Ctrl+a c: New Pane\n"
			helpMsg += "  Ctrl+a n: Next Pane\n"
			helpMsg += "  Ctrl+a p: Previous Pane\n"
			helpMsg += "  Ctrl+a &: Kill Pane\n"
			helpMsg += "  Ctrl+a \": Split Horizontal (New Pane)\n"
			helpMsg += "  Ctrl+a o: Next Pane (same as Ctrl+a n)\n"
			helpMsg += "  Ctrl+a ?: Show Help\n"
			sm.redrawWithContent(helpMsg)
		}
		sm.session.mutex.Unlock()
	}
}

func (s *Session) redraw() {
	// For now, we just clear the screen and show the active pane number
	status := fmt.Sprintf("\033[7m Pane: %d \033[0m\n", s.activePane)
	s.Broadcast(s.createRedrawMessage(status))
}

func (sm *SessionManager) redrawWithContent(content string) {
	// Send redraw message to this specific client
	sm.conn.Write(sm.session.createRedrawMessage(content))
}

func (s *Session) createRedrawMessage(content string) []byte {
	header := make([]byte, 5)
	header[0] = 0x08 // redraw
	binary.BigEndian.PutUint32(header[1:], uint32(len(content)))
	return append(header, []byte(content)...)
}

func (s *Session) RemovePane(id int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for i, p := range s.panes {
		if p.id == id {
			p.Close()
			s.panes = append(s.panes[:i], s.panes[i+1:]...)
			if s.activePane >= len(s.panes) {
				s.activePane = len(s.panes) - 1
			}
			if s.activePane < 0 {
				// No more panes, maybe close the session or create a new one
				// For now, let's create a new one to keep the session alive
				s.NewPane()
			}
			s.redraw()
			return
		}
	}
}