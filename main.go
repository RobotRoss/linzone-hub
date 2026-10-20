package main

import (
	"embed"
	"log"

	linzoneapp "github.com/patyhank/linzone-hub/internal/app"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed frontend/public/inzone-assets/resources/app_notify_icon.png
var appIcon []byte

func main() {
	// The service pushes headset notifications to the frontend through app.Event. app is assigned
	// before app.Run, and the service only emits once the frontend has started calling it.
	var app *application.App
	service := linzoneapp.NewServiceWithEmitter(func(name string, payload any) {
		if app != nil {
			app.Event.Emit(name, payload)
		}
	})
	app = application.New(application.Options{
		Name:        "LINZONE Hub",
		Description: "Linux GUI controller for Sony INZONE headsets",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(service),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "LINZONE Hub",
		Width:            1180,
		Height:           760,
		MinWidth:         920,
		MinHeight:        640,
		BackgroundColour: application.NewRGB(9, 12, 19),
		URL:              "/",
		Linux: application.LinuxWindow{
			Icon: appIcon,
		},
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
