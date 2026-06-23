package session

type CreateSessionRequest struct {
	ProxyURL string `json:"proxy_url"`
}

type SessionResponse struct {
	ID         string `json:"id"`
	Phone      string `json:"phone"`
	Status     string `json:"status"`
	DailyCount int    `json:"daily_count"`
	ProxyURL   string `json:"proxy_url,omitempty"`
	LastActive string `json:"last_active,omitempty"`
	CreatedAt  string `json:"created_at"`
}
