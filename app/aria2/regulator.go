package aria2

import component "github.com/snakexgc/tdl/application/downloader.aria2"

type (
	TelegramErrorRegulatorClient = component.TelegramErrorRegulatorClient
	TelegramErrorRegulator       = component.TelegramErrorRegulator
)

var NewTelegramErrorRegulator = component.NewTelegramErrorRegulator
