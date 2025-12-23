package main

import (
	"fmt"
	"os"
)

func main() {
	defaultNick := "User"
	if len(os.Args) > 1 {
		defaultNick = os.Args[1]
	}

	node, err := NewP2PNode(defaultNick)
	if err != nil {
		fmt.Printf("Error creating P2P node: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Starting P2P Node (UUID: %s)...\n", node.StableID)
	if err := node.Start(); err != nil {
		fmt.Printf("Error starting P2P services: %v\n", err)
		os.Exit(1)
	}

	node.JoinChannel("global-chat", "")

	gui := NewGUI(node)
	gui.Run()
}