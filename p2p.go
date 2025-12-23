package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

var (
	DiscoveryServiceTag = "p2p-chat-v3" // Updated tag for V3
	DataFile            = "data.json"
)

// ChatMessage: The actual content
type ChatMessage struct {
	ID         string `json:"id"`
	ChannelID  string `json:"channel_id"`
	SenderID   string `json:"sender_id"`
	SenderName string `json:"sender_name"`
	Content    string `json:"content"`
	Timestamp  int64  `json:"timestamp"`
}

// WireMessage: The envelope (Plaintext OR Encrypted)
type WireMessage struct {
	Type    string `json:"type"`            // "plaintext" or "encrypted"
	Payload string `json:"payload"`         // JSON(ChatMessage) OR Hex(EncryptedBlob)
	Nonce   string `json:"nonce,omitempty"` // For encryption
}

type Channel struct {
	ID       string
	Password string // New in V3
	Topic    *pubsub.Topic
	Sub      *pubsub.Subscription
	Messages []ChatMessage
	Unread   int
}

// SavedData: Struct for JSON persistence
type SavedData struct {
	Nick     string `json:"nick"`
	Channels []struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	} `json:"channels"`
}

type P2PNode struct {
	Host           host.Host
	PubSub         *pubsub.PubSub
	Ctx            context.Context
	Cancel         context.CancelFunc
	Channels       map[string]*Channel
	Nick           string
	StableID       string 
	ChannelUpdates chan string
	Mutex          sync.RWMutex
}

func NewP2PNode(defaultNick string) (*P2PNode, error) {
	ctx, cancel := context.WithCancel(context.Background())

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"))
	if err != nil {
		cancel()
		return nil, err
	}

	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		cancel()
		return nil, err
	}

	// Load or Generate Stable Identity
	stableID, err := getStableIdentity()
	if err != nil {
		cancel()
		return nil, err
	}

	node := &P2PNode{
		Host:           h,
		PubSub:         ps,
		Ctx:            ctx,
		Cancel:         cancel,
		Channels:       make(map[string]*Channel),
		Nick:           defaultNick,
		StableID:       stableID,
		ChannelUpdates: make(chan string, 100),
	}

	// Load persistent data (Nick, Channels)
	node.LoadData()

	return node, nil
}

func (n *P2PNode) Start() error {
	s := mdns.NewMdnsService(n.Host, DiscoveryServiceTag, &discoveryNotifee{h: n.Host})
	return s.Start()
}

// JoinChannel updated to accept Password
func (n *P2PNode) JoinChannel(channelID, password string) error {
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
		Password: password, // Store password
		Topic:    topic,
		Sub:      sub,
		Messages: []ChatMessage{},
	}

	n.Channels[channelID] = ch
	go n.readLoop(ch)
	
	// Auto-save when joining
	go n.SaveData()

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
	go n.SaveData()
}

// SendMessage updated to handle Encryption
func (n *P2PNode) SendMessage(channelID, content string) error {
	n.Mutex.RLock()
	ch, exists := n.Channels[channelID]
	n.Mutex.RUnlock()

	if !exists {
		return errors.New("channel not found")
	}

	chatMsg := ChatMessage{
		ID:         uuid.New().String(),
		ChannelID:  channelID,
		SenderID:   n.StableID, // Use StableID
		SenderName: n.Nick,
		Content:    content,
		Timestamp:  time.Now().Unix(),
	}

	msgBytes, err := json.Marshal(chatMsg)
	if err != nil {
		return err
	}

	var wireMsg WireMessage

	if ch.Password != "" {
		// Encrypt
		encrypted, nonce, err := encrypt(string(msgBytes), ch.Password)
		if err != nil {
			return err
		}
		wireMsg = WireMessage{Type: "encrypted", Payload: encrypted, Nonce: nonce}
	} else {
		// Plaintext
		wireMsg = WireMessage{Type: "plaintext", Payload: string(msgBytes)}
	}

	wireBytes, err := json.Marshal(wireMsg)
	if err != nil {
		return err
	}

	return ch.Topic.Publish(n.Ctx, wireBytes)
}

func (n *P2PNode) readLoop(ch *Channel) {
	for {
		msg, err := ch.Sub.Next(n.Ctx)
		if err != nil {
			return
		}
		if msg.ReceivedFrom == n.Host.ID() {
			continue
		}

		var wireMsg WireMessage
		if err := json.Unmarshal(msg.Data, &wireMsg); err != nil {
			continue
		}

		var payloadBytes []byte

		if wireMsg.Type == "encrypted" {
			if ch.Password == "" {
				continue // Cannot decrypt without password
			}
			decrypted, err := decrypt(wireMsg.Payload, wireMsg.Nonce, ch.Password)
			if err != nil {
				fmt.Println("Decryption failed:", err)
				continue
			}
			payloadBytes = []byte(decrypted)
		} else {
			payloadBytes = []byte(wireMsg.Payload)
		}

		var chatMsg ChatMessage
		if err := json.Unmarshal(payloadBytes, &chatMsg); err != nil {
			continue
		}

		n.Mutex.Lock()
		ch.Messages = append(ch.Messages, chatMsg)
		ch.Unread++
		n.Mutex.Unlock()

		n.ChannelUpdates <- ch.ID
	}
}

// --- Persistence ---

func (n *P2PNode) SaveData() {
	n.Mutex.RLock()
	defer n.Mutex.RUnlock()

	data := SavedData{
		Nick: n.Nick,
		Channels: make([]struct {
			ID       string `json:"id"`
			Password string `json:"password"`
		}, 0, len(n.Channels)),
	}

	for _, ch := range n.Channels {
		data.Channels = append(data.Channels, struct {
			ID       string `json:"id"`
			Password string `json:"password"`
		}{ID: ch.ID, Password: ch.Password})
	}

	bytes, _ := json.MarshalIndent(data, "", "  ")
	_ = ioutil.WriteFile(DataFile, bytes, 0644)
}

func (n *P2PNode) LoadData() {
	bytes, err := ioutil.ReadFile(DataFile)
	if err != nil {
		return
	}

	var data SavedData
	if err := json.Unmarshal(bytes, &data); err != nil {
		return
	}

	if data.Nick != "" {
		n.Nick = data.Nick
	}


	for _, chData := range data.Channels {
		go n.JoinChannel(chData.ID, chData.Password)
	}
}

// --- Helpers ---

func getStableIdentity() (string, error) {
	if _, err := os.Stat("identity.key"); err == nil {
		data, err := ioutil.ReadFile("identity.key")
		return string(data), err
	}
	id := uuid.New().String()
	_ = ioutil.WriteFile("identity.key", []byte(id), 0600)
	return id, nil
}

func encrypt(plaintext, password string) (string, string, error) {
	key := sha256.Sum256([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), hex.EncodeToString(nonce), nil
}

func decrypt(ciphertextHex, nonceHex, password string) (string, error) {
	key := sha256.Sum256([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ciphertext, err := hex.DecodeString(ciphertextHex)
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	// GCM Open handles authentication tag check
	plaintext, err := gcm.Open(nil, nonce, ciphertext[gcm.NonceSize():], nil)
	return string(plaintext), err
}

type discoveryNotifee struct{ h host.Host }
func (d *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	if pi.ID != d.h.ID() { d.h.Connect(context.Background(), pi) }
}