package types

type NotificationMessage struct {
	Account   AccountID `json:"account"`
	ChatID    int64     `json:"chat_id"`
	MessageID int       `json:"message_id"`
}
