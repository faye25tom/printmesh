//go:build windows && !win7

package main

import (
	"log"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"printmesh/frontend"
)

var Version = "dev"

func main() {
	startHidden := false
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(arg, "--minimized") {
			startHidden = true
			break
		}
	}

	app := NewApp(Version)

	err := wails.Run(&options.App{
		Title:            "PrintMesh - 异地组网共享打印",
		Width:            940,
		Height:           660,
		MinWidth:         760,
		MinHeight:        520,
		StartHidden:      startHidden,
		BackgroundColour: &options.RGBA{R: 15, G: 23, B: 42, A: 255},
		AssetServer: &assetserver.Options{
			Assets: frontend.DistFS(),
		},
		OnStartup: app.startup,
		Bind: []any{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.SystemDefault,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
