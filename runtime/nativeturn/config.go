package nativeturn

import (
	"fmt"
	"strings"
	"time"
)

type Config struct {
	TurnDeadline time.Duration
	GraceWindow  time.Duration
	JournalSize  int
}

const (
	DefaultTurnDeadline = 15 * time.Minute
	DefaultGraceWindow  = 60 * time.Second
	DefaultJournalSize  = 512
)

func ParseEnv(turnMax, turnGrace string) (Config, error) {
	cfg := Config{TurnDeadline: DefaultTurnDeadline, GraceWindow: DefaultGraceWindow}

	if raw := strings.TrimSpace(turnMax); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("nativeturn: invalid CONTENOX_TURN_MAX %q: must be a positive Go duration (e.g. 15m)", turnMax)
		}
		cfg.TurnDeadline = d
	}

	if raw := strings.TrimSpace(turnGrace); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("nativeturn: invalid CONTENOX_TURN_GRACE %q: must be a positive Go duration (e.g. 60s)", turnGrace)
		}
		cfg.GraceWindow = d
	}

	return cfg, nil
}
