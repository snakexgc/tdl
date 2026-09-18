package aria2

import component "github.com/snakexgc/tdl/application/downloader.aria2"

type ReconnectClient = component.ReconnectClient

var (
	SuspendTDLTasksForReconnect = component.SuspendTDLTasksForReconnect
	ResumeTDLTasks              = component.ResumeTDLTasks
	PauseTDLTasksForShutdown    = component.PauseTDLTasksForShutdown
	ResumeStartupPausedTasks    = component.ResumeStartupPausedTasks
	IsTDLTask                   = component.IsTDLTask
	RetryConnection             = component.RetryConnection
	RetryConnectionWithInterval = component.RetryConnectionWithInterval
	DownloadURLPrefix           = component.DownloadURLPrefix
	MergeUniqueGIDs             = component.MergeUniqueGIDs
	UniqueGIDs                  = component.UniqueGIDs
)

const DefaultConnectRetryInterval = component.DefaultConnectRetryInterval
