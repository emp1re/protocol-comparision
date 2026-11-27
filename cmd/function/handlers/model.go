package handlers

import "time"

type payloadInfo struct {
	Payload []byte     `json:"-"`
	Time    *time.Time `json:"time,omitempty"`
	TimeEnd *time.Time `json:"timeEnd,omitempty"`
	Type    string     `json:"type"`
}

type jsonPayload struct {
	Payload *string    `json:"payload"`
	Time    *time.Time `json:"time"`
	TimeEnd *time.Time `json:"timeEnd"`
	Type    string     `json:"type"`
}
