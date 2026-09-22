package agent

import "time"

type CompanionFrame struct {
	Type         string             `json:"type"`
	RequestID    string             `json:"request_id,omitempty"`
	DeviceID     string             `json:"device_id,omitempty"`
	Token        string             `json:"token,omitempty"`
	Capabilities []DeviceCapability `json:"capabilities,omitempty"`
	Payload      map[string]any     `json:"payload,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
}

type CompanionWelcome struct {
	Type      string    `json:"type"`
	DeviceID  string    `json:"device_id"`
	Protocol  string    `json:"protocol"`
	ServerNow time.Time `json:"server_now"`
}
