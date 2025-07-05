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
	fmt.Printf("Session %s: New pane created with ID %d. Active pane: %d\n", s.id, p.id, s.activePane)

	// Start a goroutine to read from the new pane and broadcast
	go func(pane *Pane) {
		for output := range pane.output {
			// Send data message with pane ID as 4-byte prefix
			payload := make([]byte, 4+len(output))
			binary.BigEndian.PutUint32(payload[:4], uint32(pane.id))
			copy(payload[4:], output)
			
			header := make([]byte, 5)
			header[0] = 0x00 // data
			binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
			msg := append(header, payload...)
			s.Broadcast(msg)
		}
	}(p)

	// Notify clients about the new pane and active pane switch
	payload, _ := json.Marshal(p.id) // Send the new pane ID
	header := make([]byte, 5)
	header[0] = 0x0A // new pane notification
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	msg := append(header, payload...)
	fmt.Printf("Session: Broadcasting new pane notification for pane %d, message length %d\n", p.id, len(msg))
	s.Broadcast(msg)

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
				fmt.Printf("SessionManager: Writing %d bytes to active pane %d\n", len(payload), sm.session.activePane) // Debug print
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
			fmt.Println("SessionManager: Received new window command (creating new pane)") // Debug print
			sm.session.NewPane()
		case 0x03: // next window (now next pane)
			if len(sm.session.panes) > 0 {
				sm.session.activePane = (sm.session.activePane + 1) % len(sm.session.panes)
				fmt.Printf("SessionManager: Switched to next pane: %d\n", sm.session.activePane) // Debug print
				sm.session.switchPane(sm.session.panes[sm.session.activePane].id)
			}
		case 0x04: // prev window (now prev pane)
			if len(sm.session.panes) > 0 {
				sm.session.activePane = (sm.session.activePane - 1 + len(sm.session.panes)) % len(sm.session.panes)
				fmt.Printf("SessionManager: Switched to previous pane: %d\n", sm.session.activePane) // Debug print
				sm.session.switchPane(sm.session.panes[sm.session.activePane].id)
			}
		case 0x05: // kill window (now kill pane)
			if len(sm.session.panes) > 0 {
				sm.session.RemovePane(sm.session.panes[sm.session.activePane].id)
			}
		case 0x06: // split horizontal (already handled by NewPane)
			fmt.Println("SessionManager: Received split horizontal command (creating new pane)")
			sm.session.mutex.Unlock() // Release mutex before calling NewPane to avoid deadlock
			pane, err := sm.session.NewPane()
			if err != nil {
				fmt.Printf("SessionManager: Error creating new pane: %v\n", err)
			} else {
				fmt.Printf("SessionManager: Successfully created new pane with ID %d\n", pane.id)
			}
			continue // Skip the mutex.Unlock() at the end since we already unlocked
		case 0x07: // next pane (already handled by 0x03/0x04)
			// This case is now redundant with 0x03/0x04, but keeping for now.
			if len(sm.session.panes) > 0 {
				sm.session.activePane = (sm.session.activePane + 1) % len(sm.session.panes)
				sm.session.switchPane(sm.session.panes[sm.session.activePane].id)
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
	status := fmt.Sprintf("Pane: %d", s.activePane)
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

func (s *Session) switchPane(paneID int) {
	// Send pane switch notification to clients
	payload, _ := json.Marshal(paneID)
	header := make([]byte, 5)
	header[0] = 0x0B // switch pane notification
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	msg := append(header, payload...)
	s.Broadcast(msg)
	s.redraw()
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
