package missionservice

const AttentionAskedSubject = "missionservice.events.attention_asked"

type AttentionAskedEvent struct {
	MissionID       string `json:"missionId"`
	AskID           string `json:"askId"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	AgentName       string `json:"agentName,omitempty"`
	Intent          string `json:"intent,omitempty"`
	Summary         string `json:"summary"`
	Detail          string `json:"detail,omitempty"`
}
