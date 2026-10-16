// SPDX-License-Identifier: MIT

package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/ui
var assets embed.FS

func main() {
	// Started with --supervise, this process is a detached job runner, not a
	// window: it must never reach wails.Run.
	if runSupervisorIfRequested() {
		return
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "RsyncGUI",
		Width:     960,
		Height:    640,
		MinWidth:  780,
		MinHeight: 540,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 14, G: 16, B: 22, A: 255},
		OnStartup:        app.startup,
		Linux: &linux.Options{
			Icon: windowIcon(appIcon), // ridotta: vedi icon_linux.go
		},
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
			About: &mac.AboutInfo{
				Title:   "RsyncGUI 2.5.2",
				Message: "GUI leggera e multipiattaforma per rsync",
			},
		},
	})
	if err != nil {
		panic(err)
	}
}
