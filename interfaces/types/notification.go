package types

const NotificationRequested = "notification.requested"

type NotificationRequest struct {
	Text string `json:"text"`
}

type NotificationMessage struct {
	Account   AccountID `json:"account"`
	ChatID    int64     `json:"chat_id"`
	MessageID int       `json:"message_id"`
}
