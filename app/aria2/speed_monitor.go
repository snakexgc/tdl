package aria2

import component "github.com/snakexgc/tdl/application/downloader.aria2"

type (
	ZeroSpeedMonitorClient = component.ZeroSpeedMonitorClient
	ZeroSpeedMonitor       = component.ZeroSpeedMonitor
)

var NewZeroSpeedMonitor = component.NewZeroSpeedMonitor
