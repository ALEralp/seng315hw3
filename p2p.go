package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/core/peer"
)

var DiscoveryServiceTag = "p2p-chat-v1"

type ChatMessage struct {
	SenderID   string `json:"sender_id"`
	SenderName string `json:"sender_name"`
	Content    string `json:"content"`
}

type P2PNode struct {
	Host           host.Host
	PubSub         *pubsub.PubSub
	Ctx            context.Context
	Topic          *pubsub.Topic
	Sub            *pubsub.Subscription
	Nick           string
	Messages       []ChatMessage
	MessageChan    chan ChatMessage
	Mutex          sync.Mutex
}

func NewP2PNode(nick string) (*P2PNode, error) {
	ctx := context.Background()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"))
	if err != nil {
		return nil, err
	}

	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return nil, err
	}

	return &P2PNode{
		Host:        h,
		PubSub:      ps,
		Ctx:         ctx,
		Nick:        nick,
		Messages:    make([]ChatMessage, 0),
		MessageChan: make(chan ChatMessage, 10),
	}, nil
}

func (n *P2PNode) Start() error {
	// Simple mDNS discovery
	s := mdns.NewMdnsService(n.Host, DiscoveryServiceTag, &discoveryNotifee{h: n.Host})
	if err := s.Start(); err != nil {
		return err
	}

	// Join Global Chat immediately
	topic, err := n.PubSub.Join("global-chat")
	if err != nil {
		return err
	}
	sub, err := topic.Subscribe()
	if err != nil {
		return err
	}

	n.Topic = topic
	n.Sub = sub

	go n.readLoop()
	return nil
}

func (n *P2PNode) SendMessage(content string) error {
	msg := ChatMessage{
		SenderID:   n.Host.ID().ShortString(),
		SenderName: n.Nick,
		Content:    content,
	}
	bytes, _ := json.Marshal(msg)
	return n.Topic.Publish(n.Ctx, bytes)
}

func (n *P2PNode) readLoop() {
	for {
		msg, err := n.Sub.Next(n.Ctx)
		if err != nil {
			return
		}
		if msg.ReceivedFrom == n.Host.ID() {
			continue
		}
		var cm ChatMessage
		json.Unmarshal(msg.Data, &cm)
		n.MessageChan <- cm
	}
}

type discoveryNotifee struct {
	h host.Host
}

func (d *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	if pi.ID != d.h.ID() {
		d.h.Connect(context.Background(), pi)
	}
}