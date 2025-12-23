package main

import (
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type GUI struct {
	App  fyne.App
	Win  fyne.Window
	Node *P2PNode
	Box  *fyne.Container
}

func NewGUI(node *P2PNode) *GUI {
	a := app.New()
	w := a.NewWindow("P2P Chat v1")
	w.Resize(fyne.NewSize(600, 400))

	return &GUI{App: a, Win: w, Node: node}
}

func (g *GUI) Run() {
	g.Box = container.NewVBox()
	
	input := widget.NewEntry()
	input.PlaceHolder = "Type..."
	input.OnSubmitted = func(s string) {
		g.Node.SendMessage(s)
		g.Box.Add(widget.NewLabel(fmt.Sprintf("Me: %s", s)))
		input.SetText("")
	}

	scroll := container.NewScroll(g.Box)
	content := container.NewBorder(nil, input, nil, nil, scroll)
	g.Win.SetContent(content)

	go func() {
		for msg := range g.Node.MessageChan {
			g.Box.Add(widget.NewLabel(fmt.Sprintf("%s: %s", msg.SenderName, msg.Content)))
		}
	}()

	g.Win.ShowAndRun()
}