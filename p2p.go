package main

import (
	"context"
	"encoding/json"
	"errors"
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

// DiscoveryServiceTag is unique to V2 to avoid mixing with V4 peers
var DiscoveryServiceTag = "p2p-chat-v2"

// ChatMessage is the plain data sent over the network
type ChatMessage struct {
	ID         string `json:"id"`
	ChannelID  string `json:"channel_id"`
	SenderID   string `json:"sender_id"`
	SenderName string `json:"sender_name"`
	Content    string `json:"content"`
	Timestamp  int64  `json:"timestamp"`
}

type Channel struct {
	ID       string
	Topic    *pubsub.Topic
	Sub      *pubsub.Subscription
	Messages []ChatMessage
	Unread   int
}

type P2PNode struct {
	Host           host.Host
	PubSub         *pubsub.PubSub
	Ctx            context.Context
	Cancel         context.CancelFunc
	Channels       map[string]*Channel
	Nick           string
	ID             string // Simple ephemeral ID for V2
	ChannelUpdates chan string
	Mutex          sync.RWMutex
}

func NewP2PNode(nick string) (*P2PNode, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Basic Host Setup
	h, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"),
	)
	if err != nil {
		cancel()
		return nil, err
	}

	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		cancel()
		return nil, err
	}

	return &P2PNode{
		Host:           h,
		PubSub:         ps,
		Ctx:            ctx,
		Cancel:         cancel,
		Channels:       make(map[string]*Channel),
		Nick:           nick,
		ID:             h.ID().ShortString(),
		ChannelUpdates: make(chan string, 100),
	}, nil
}

func (n *P2PNode) Start() error {
	// Setup MDNS for local discovery
	s := mdns.NewMdnsService(n.Host, DiscoveryServiceTag, &discoveryNotifee{h: n.Host})
	return s.Start()
}

// JoinChannel adds the logic to support multiple rooms
func (n *P2PNode) JoinChannel(channelID string) error {
	n.Mutex.Lock()
	defer n.Mutex.Unlock()

	if _, exists := n.Channels[channelID]; exists {
		return nil
	}

	topic, err := n.PubSub.Join(channelID)
	if err != nil {
		return err
	}

	sub, err := topic.Subscribe()
	if err != nil {
		topic.Close()
		return err
	}

	ch := &Channel{
		ID:       channelID,
		Topic:    topic,
		Sub:      sub,
		Messages: []ChatMessage{},
	}

	n.Channels[channelID] = ch
	go n.readLoop(ch)
	
	return nil
}

func (n *P2PNode) LeaveChannel(channelID string) {
	n.Mutex.Lock()
	defer n.Mutex.Unlock()

	if ch, exists := n.Channels[channelID]; exists {
		ch.Sub.Cancel()
		ch.Topic.Close()
		delete(n.Channels, channelID)
	}
}

// SendMessage sends plain JSON (Encryption added in V3)
func (n *P2PNode) SendMessage(channelID, content string) error {
	n.Mutex.RLock()
	ch, exists := n.Channels[channelID]
	n.Mutex.RUnlock()

	if !exists {
		return errors.New("channel not found")
	}

	msg := ChatMessage{
		ID:         uuid.New().String(),
		ChannelID:  channelID,
		SenderID:   n.ID,
		SenderName: n.Nick,
		Content:    content,
		Timestamp:  time.Now().Unix(),
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return ch.Topic.Publish(n.Ctx, msgBytes)
}

func (n *P2PNode) readLoop(ch *Channel) {
	for {
		msg, err := ch.Sub.Next(n.Ctx)
		if err != nil {
			return
		}

		// Don't process our own messages from the network loop
		if msg.ReceivedFrom == n.Host.ID() {
			continue
		}

		var chatMsg ChatMessage
		if err := json.Unmarshal(msg.Data, &chatMsg); err != nil {
			continue
		}

		n.Mutex.Lock()
		ch.Messages = append(ch.Messages, chatMsg)
		ch.Unread++
		n.Mutex.Unlock()

		n.ChannelUpdates <- ch.ID
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