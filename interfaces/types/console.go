package types

type ConsoleCommand struct {
	Port        string
	Owner       string
	Aliases     []string
	Name        string
	Description string
}

type ConsoleRequest struct {
	Account               AccountID
	UserID, ChatID        int64
	MessageID             int
	Name, Text, ReplyText string
	Private               bool
}
type ConsoleResponse struct{ Text string }
