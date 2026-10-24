package missionservice

import (
	"encoding/json"
	"strings"
)

const MissionMetaKey = "contenox.mission"

type MissionMeta struct {
	MissionID string `json:"missionId"`
}

func MarshalMissionMeta(missionID string) json.RawMessage {
	if strings.TrimSpace(missionID) == "" {
		return nil
	}
	raw, err := json.Marshal(map[string]MissionMeta{MissionMetaKey: {MissionID: missionID}})
	if err != nil {
		return nil
	}
	return raw
}

func ParseMissionMeta(meta json.RawMessage) (string, bool) {
	if len(meta) == 0 {
		return "", false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(meta, &m) != nil {
		return "", false
	}
	raw, ok := m[MissionMetaKey]
	if !ok {
		return "", false
	}
	var mm MissionMeta
	if json.Unmarshal(raw, &mm) != nil {
		return "", false
	}
	id := strings.TrimSpace(mm.MissionID)
	if id == "" {
		return "", false
	}
	return id, true
}
