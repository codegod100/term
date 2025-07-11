package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
)

type ClientState struct {
	paneBuffers  map[int]*PaneBuffer
	activePaneID int
	status       string
	ui           *UI
}

func NewClientState(ui *UI) *ClientState {
	width, height := ui.Size()
	paneBuffers := make(map[int]*PaneBuffer)
	activePaneID := 0
	
	// Create initial pane buffer
	paneBuffers[activePaneID] = NewPaneBuffer(width, height-1) // -1 for status line
	
	return &ClientState{
		paneBuffers:  paneBuffers,
		activePaneID: activePaneID,
		status:       fmt.Sprintf("Pane: %d", activePaneID),
		ui:           ui,
	}
}

func (cs *ClientState) HandleDataMessage(payload []byte) {
	if len(payload) >= 4 {
		paneID := int(binary.BigEndian.Uint32(payload[:4]))
		data := payload[4:]
		
		// Debug: log data messages
		if f, err := os.OpenFile("/tmp/term-client.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			f.WriteString(fmt.Sprintf("HandleDataMessage: paneID=%d, activePaneID=%d, bufferExists=%t, dataLen=%d\n", 
				paneID, cs.activePaneID, cs.paneBuffers[paneID] != nil, len(data)))
			f.Close()
		}
		
		if pb, ok := cs.paneBuffers[paneID]; ok {
			pb.Write(data)
			// Only redraw if this is the active pane
			if paneID == cs.activePaneID {
				cs.Draw()
			}
		} else {
			// Debug: log missing buffer
			if f, err := os.OpenFile("/tmp/term-client.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				f.WriteString(fmt.Sprintf("HandleDataMessage: No buffer found for pane %d, available buffers: %v\n", 
					paneID, cs.getBufferKeys()))
				f.Close()
			}
		}
	}
}

func (cs *ClientState) getBufferKeys() []int {
	keys := make([]int, 0, len(cs.paneBuffers))
	for k := range cs.paneBuffers {
		keys = append(keys, k)
	}
	return keys
}

func (cs *ClientState) HandleRedrawMessage(payload []byte) {
	cs.status = string(payload)
	cs.Draw()
}

func (cs *ClientState) HandleNewPaneMessage(payload []byte) {
	var newPaneID int
	if err := json.Unmarshal(payload, &newPaneID); err == nil {
		// Debug: write to log file since we can't use fmt.Printf in TUI
		if f, err := os.OpenFile("/tmp/term-client.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			f.WriteString(fmt.Sprintf("Client: Received new pane notification, ID=%d, old active=%d\n", newPaneID, cs.activePaneID))
			f.Close()
		}
		
		width, height := cs.ui.Size()
		cs.paneBuffers[newPaneID] = NewPaneBuffer(width, height-1)
		cs.activePaneID = newPaneID // Switch to new pane
		cs.status = fmt.Sprintf("Pane: %d", cs.activePaneID)
		cs.Draw()
	} else {
		// Debug: log error
		if f, err := os.OpenFile("/tmp/term-client.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			f.WriteString(fmt.Sprintf("Client: Error unmarshaling new pane ID: %v\n", err))
			f.Close()
		}
	}
}

func (cs *ClientState) HandleSwitchPaneMessage(payload []byte) {
	var targetPaneID int
	if err := json.Unmarshal(payload, &targetPaneID); err == nil {
		cs.activePaneID = targetPaneID
		cs.status = fmt.Sprintf("Pane: %d", cs.activePaneID)
		cs.Draw()
	}
}

func (cs *ClientState) UpdatePaneBufferSizes() {
	width, height := cs.ui.Size()
	for _, pb := range cs.paneBuffers {
		pb.width = width
		pb.height = height - 1 // -1 for status line
		// Resize the vt10x terminal
		pb.terminal.Resize(width, height-1)
	}
}

func (cs *ClientState) Draw() {
	cs.ui.DrawScreen(cs.paneBuffers, cs.activePaneID, cs.status)
}

func (cs *ClientState) GetActivePaneID() int {
	return cs.activePaneID
}