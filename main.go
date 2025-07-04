package main

import (
	"os"
)

const (
	prefixKey = '\x01' // Ctrl+a
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		runDaemon()
	} else {
		runClient()
	}
}