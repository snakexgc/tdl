package types

type TelegramApp struct {
	AppID   int
	AppHash string
}

type TelegramCredentialsConfig struct {
	APIID         int    `json:"api_id"`
	APIHash       string `json:"api_hash"`
	BuiltinPreset string `json:"builtin_preset"`
	UseBuiltin    bool   `json:"use_builtin"`
}

type TelegramCredentials struct {
	App    TelegramApp
	Preset string
}
