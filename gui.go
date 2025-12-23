package main

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type GUI struct {
	App           fyne.App
	Win           fyne.Window
	Node          *P2PNode
	
	// UI Components
	ChatContainer *fyne.Container
	MsgInput      *widget.Entry
	ChannelList   *widget.List
	
	ActiveChannel string
}

func NewGUI(node *P2PNode) *GUI {
	a := app.New()
	w := a.NewWindow("P2P Chat v2 - " + node.Nick)
	w.Resize(fyne.NewSize(900, 600))

	gui := &GUI{
		App:           a,
		Win:           w,
		Node:          node,
		ActiveChannel: "global-chat",
	}

	gui.setupUI()
	return gui
}

func (g *GUI) Run() {
	go g.updateLoop()
	g.Win.ShowAndRun()
}

func (g *GUI) setupUI() {
	// --- Sidebar (Channels Only) ---
	g.ChannelList = widget.NewList(
		func() int {
			g.Node.Mutex.RLock()
			defer g.Node.Mutex.RUnlock()
			return len(g.Node.Channels)
		},
		func() fyne.CanvasObject {
			label := widget.NewLabel("Channel")
			return container.NewPadded(label)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			g.Node.Mutex.RLock()
			keys := make([]string, 0, len(g.Node.Channels))
			for k := range g.Node.Channels {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			
			id := keys[i]
			ch := g.Node.Channels[id]
			unread := ch.Unread
			g.Node.Mutex.RUnlock()

			// Simple container logic
			c := o.(*fyne.Container)
			label := c.Objects[0].(*widget.Label)
			
			txt := id
			if unread > 0 {
				txt = fmt.Sprintf("%s (%d)", id, unread)
			}
			label.SetText(txt)
			if id == g.ActiveChannel {
				label.TextStyle = fyne.TextStyle{Bold: true}
			} else {
				label.TextStyle = fyne.TextStyle{}
			}
		},
	)

	g.ChannelList.OnSelected = func(id widget.ListItemID) {
		g.Node.Mutex.RLock()
		keys := make([]string, 0, len(g.Node.Channels))
		for k := range g.Node.Channels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		selectedID := keys[id]
		g.Node.Mutex.RUnlock()

		g.ActiveChannel = selectedID
		g.refreshChat()
		g.ChannelList.Refresh()
	}

	addChannelBtn := widget.NewButtonWithIcon("Join Channel", theme.ContentAddIcon(), func() {
		g.showAddChannelDialog()
	})

	sidebar := container.NewBorder(
		widget.NewLabelWithStyle("Channels", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}), 
		addChannelBtn, 
		nil, nil, 
		g.ChannelList,
	)

	// --- Chat Area ---
	g.ChatContainer = container.NewVBox()
	scroll := container.NewScroll(g.ChatContainer)

	g.MsgInput = widget.NewEntry()
	g.MsgInput.PlaceHolder = "Type a message..."
	g.MsgInput.OnSubmitted = func(s string) {
		if s == "" { return }
		
		if err := g.Node.SendMessage(g.ActiveChannel, s); err != nil {
			dialog.ShowError(err, g.Win)
			return
		}

		// Optimistic UI Update
		g.Node.Mutex.Lock()
		if ch, ok := g.Node.Channels[g.ActiveChannel]; ok {
			ch.Messages = append(ch.Messages, ChatMessage{
				SenderID:   g.Node.ID,
				SenderName: g.Node.Nick,
				Content:    s,
				Timestamp:  time.Now().Unix(),
			})
		}
		g.Node.Mutex.Unlock()
		
		g.MsgInput.SetText("")
		g.refreshChat()
	}

	sendBtn := widget.NewButtonWithIcon("", theme.MailSendIcon(), func() {
		g.MsgInput.OnSubmitted(g.MsgInput.Text)
	})

	inputBar := container.NewBorder(nil, nil, nil, sendBtn, g.MsgInput)
	mainContent := container.NewBorder(nil, inputBar, nil, nil, scroll)

	// Split View
	split := container.NewHSplit(sidebar, mainContent)
	split.SetOffset(0.3)

	g.Win.SetContent(split)
}

func (g *GUI) showAddChannelDialog() {
	input := widget.NewEntry()
	input.PlaceHolder = "Channel Name/ID"
	
	dialog.ShowCustomConfirm("Join Channel", "Join", "Cancel", input, func(ok bool) {
		if ok && input.Text != "" {
			name := strings.TrimSpace(input.Text)
			g.Node.JoinChannel(name)
			g.ChannelList.Refresh()
		}
	}, g.Win)
}

func (g *GUI) refreshChat() {
	g.ChatContainer.Objects = nil

	g.Node.Mutex.RLock()
	ch, exists := g.Node.Channels[g.ActiveChannel]
	g.Node.Mutex.RUnlock()

	if !exists {
		g.ChatContainer.Add(widget.NewLabel("Channel not found"))
		g.ChatContainer.Refresh()
		return
	}

	// Mark read
	g.Node.Mutex.Lock()
	ch.Unread = 0
	g.Node.Mutex.Unlock()

	for _, msg := range ch.Messages {
		isMe := msg.SenderID == g.Node.ID
		g.ChatContainer.Add(g.createBubble(msg, isMe))
	}
	g.ChatContainer.Refresh()
}

func (g *GUI) createBubble(msg ChatMessage, isMe bool) fyne.CanvasObject {
	ts := time.Unix(msg.Timestamp, 0).Format("15:04")
	nameLabel := canvas.NewText(fmt.Sprintf("%s (%s)", msg.SenderName, msg.SenderID[:4]), color.Gray{Y: 100})
	nameLabel.TextSize = 10
	nameLabel.TextStyle = fyne.TextStyle{Bold: true}
	
	timeLabel := canvas.NewText(ts, color.Gray{Y: 150})
	timeLabel.TextSize = 10

	header := container.NewHBox(nameLabel, timeLabel)
	content := widget.NewLabel(msg.Content)
	content.Wrapping = fyne.TextWrapWord

	bgCol := color.NRGBA{R: 45, G: 45, B: 45, A: 255}
	if isMe {
		bgCol = color.NRGBA{R: 40, G: 70, B: 40, A: 255}
	}
	
	bg := canvas.NewRectangle(bgCol)
	bg.CornerRadius = 8
	
	bubble := container.NewStack(bg, container.NewPadded(container.NewVBox(header, content)))
	
	if isMe {
		return container.NewBorder(nil, nil, layout.NewSpacer(), nil, bubble)
	}
	return container.NewBorder(nil, nil, nil, layout.NewSpacer(), bubble)
}

func (g *GUI) updateLoop() {
	for id := range g.Node.ChannelUpdates {
		if id == g.ActiveChannel {
			g.refreshChat()
		}
		g.ChannelList.Refresh()
	}
}