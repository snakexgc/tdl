package bot

import (
	aria2component "github.com/snakexgc/tdl/application/downloader.aria2"
	"github.com/snakexgc/tdl/interfaces/ports"
)

// Keyboards and delayed callbacks must resolve the same live component
// as slash commands. A saved keyboard never grants access to a stopped owner.
func componentAria2Factory(console ports.Console, resolve func(string, string) (any, error)) aria2ControllerFactory {
	return func() ports.Aria2Tasks {
		unavailable := func() ports.Aria2Tasks { return aria2component.NewController(aria2component.Options{}, nil) }
		if console != nil && !console.PrivateCommand("downloads") {
			return unavailable()
		}
		if resolve == nil {
			return unavailable()
		}
		if _, err := resolve("download.control", ports.DownloadControlName); err != nil {
			return unavailable()
		}
		value, err := resolve(aria2component.ID, ports.Aria2TasksName)
		if err != nil {
			return unavailable()
		}
		controller, ok := value.(ports.Aria2Tasks)
		if !ok || controller == nil {
			return unavailable()
		}
		return controller
	}
}
