package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/sogud/Muse/internal/core"
)

func main() {
	var store *core.ProjectStore

	mygo.App.WhenReady(func() {
		opened, err := core.OpenProjectStore()
		if err != nil {
			log.Printf("Could not open Muse projects: %v", err)
			return
		}
		store = opened

		workspace := core.NewCreativeWorkspace(store)
		settings := core.NewNativeSettings(store)
		app := newNativeUI(workspace, settings)

		window := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Muse",
			Content:   ui.View(app.view),
			Width:     1100,
			Height:    740,
			MinWidth:  860,
			MinHeight: 620,
			StateKey:  "main",
		})
		app.window = window
		app.startLocalSync(window)
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	_ = core.CloseProjectStore(store)
}
