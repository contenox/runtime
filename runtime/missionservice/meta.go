package missionservice

import (
	"encoding/json"
	"strings"
)

const MissionMetaKey = "contenox.mission"

type MissionMeta struct {
	MissionID string `json:"missionId"`
	Intent    string `json:"intent,omitempty"`
}

func MarshalMissionMeta(missionID string) json.RawMessage {
	return MarshalMissionMetaWithIntent(missionID, "")
}

func MarshalMissionMetaWithIntent(missionID, intent string) json.RawMessage {
	if strings.TrimSpace(missionID) == "" {
		return nil
	}
	raw, err := json.Marshal(map[string]MissionMeta{MissionMetaKey: {MissionID: missionID, Intent: strings.TrimSpace(intent)}})
	if err != nil {
		return nil
	}
	return raw
}

func ParseMissionMeta(meta json.RawMessage) (string, bool) {
	mm, ok := ParseMissionMetaFull(meta)
	return mm.MissionID, ok
}

func ParseMissionMetaFull(meta json.RawMessage) (MissionMeta, bool) {
	if len(meta) == 0 {
		return MissionMeta{}, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(meta, &m) != nil {
		return MissionMeta{}, false
	}
	raw, ok := m[MissionMetaKey]
	if !ok {
		return MissionMeta{}, false
	}
	var mm MissionMeta
	if json.Unmarshal(raw, &mm) != nil {
		return MissionMeta{}, false
	}
	mm.MissionID = strings.TrimSpace(mm.MissionID)
	mm.Intent = strings.TrimSpace(mm.Intent)
	if mm.MissionID == "" {
		return MissionMeta{}, false
	}
	return mm, true
}
