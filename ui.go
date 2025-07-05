package main

import (
	"github.com/gdamore/tcell/v2"
)

type UI struct {
	screen    tcell.Screen
	defStyle  tcell.Style
	statusStyle tcell.Style
}

func NewUI(screen tcell.Screen) *UI {
	defStyle := tcell.StyleDefault.Background(tcell.ColorReset).Foreground(tcell.ColorReset)
	statusStyle := defStyle.Reverse(true)
	
	return &UI{
		screen:      screen,
		defStyle:    defStyle,
		statusStyle: statusStyle,
	}
}

func (ui *UI) DrawScreen(paneBuffers map[int]*PaneBuffer, activePaneID int, status string) {
	ui.screen.Clear()
	
	width, height := ui.screen.Size()
	
	// Check if this is a multi-line status message (like help)
	lines := []string{}
	if len(status) > 0 {
		// Split status into lines
		currentLine := ""
		for _, r := range status {
			if r == '\n' {
				lines = append(lines, currentLine)
				currentLine = ""
			} else {
				currentLine += string(r)
			}
		}
		if currentLine != "" {
			lines = append(lines, currentLine)
		}
	}
	
	// If it's a multi-line message, display it as an overlay
	if len(lines) > 1 {
		// Display multi-line content in the center of the screen
		startY := (height - len(lines)) / 2
		for i, line := range lines {
			y := startY + i
			if y >= 0 && y < height {
				// Clear the line
				for x := 0; x < width; x++ {
					ui.screen.SetContent(x, y, ' ', nil, ui.statusStyle)
				}
				// Draw the line content
				for j, r := range line {
					if j < width {
						ui.screen.SetContent(j, y, r, nil, ui.statusStyle)
					}
				}
			}
		}
	} else {
		// Single line status - draw at top
		// Clear the entire status line with the status style
		for x := 0; x < width; x++ {
			ui.screen.SetContent(x, 0, ' ', nil, ui.statusStyle)
		}
		
		// Draw the status text
		for i, r := range status {
			if i < width {
				ui.screen.SetContent(i, 0, r, nil, ui.statusStyle)
			}
		}

		// Draw active pane content below status bar
		if pb, ok := paneBuffers[activePaneID]; ok {
			for y, line := range pb.content {
				for x, r := range line {
					ui.screen.SetContent(x, y+1, r, nil, ui.defStyle) // +1 for status line
				}
			}
			// Ensure cursor position is within bounds
			cursorX := pb.cursorX
			cursorY := pb.cursorY + 1 // +1 for status line
			if cursorX >= 0 && cursorX < width && cursorY >= 1 && cursorY < height {
				ui.screen.ShowCursor(cursorX, cursorY)
			}
		}
	}
	ui.screen.Show()
}

func (ui *UI) Clear() {
	ui.screen.Clear()
	ui.screen.Show()
}

func (ui *UI) Size() (int, int) {
	return ui.screen.Size()
}