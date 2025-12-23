package main

import (
	"fmt"
	"os"
)

func main() {
	// Simple CLI args for nickname
	nick := "User"
	if len(os.Args) > 1 {
		nick = os.Args[1]
	}

	node, err := NewP2PNode(nick)
	if err != nil {
		fmt.Printf("Error creating P2P node: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Starting P2P Node...")
	if err := node.Start(); err != nil {
		fmt.Printf("Error starting P2P services: %v\n", err)
		os.Exit(1)
	}

	// Auto-join global chat
	node.JoinChannel("global-chat")

	gui := NewGUI(node)
	gui.Run()
}