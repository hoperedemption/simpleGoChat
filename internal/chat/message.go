package chat

type Message struct {
	ClientID string `json:"clientID"` // ClientID of the sender.
	Text     string `json:"text"`     // The content of the message.
}
