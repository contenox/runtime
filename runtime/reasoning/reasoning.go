package reasoning

import (
	"fmt"
	"strings"
)

const (
	Auto    = "auto"
	Off     = "off"
	Minimal = "minimal"
	Low     = "low"
	Medium  = "medium"
	High    = "high"
	XHigh   = "xhigh"
	Max     = "max"

	Default = High
)

var Levels = []string{Off, Minimal, Low, Medium, High, XHigh, Max}

var valid = map[string]string{
	Auto:       Auto,
	"default":  Auto,
	Off:        Off,
	"false":    Off,
	"none":     Off,
	"disabled": Off,
	"disable":  Off,
	"no":       Off,
	"0":        Off,
	Minimal:    Minimal,
	Low:        Low,
	Medium:     Medium,
	High:       High,
	XHigh:      XHigh,
	Max:        Max,
	"true":     High,
	"on":       High,
	"yes":      High,
	"1":        High,
}

func Normalize(v string) (string, error) {
	level := strings.ToLower(strings.TrimSpace(v))
	if normalized, ok := valid[level]; ok {
		return normalized, nil
	}
	return "", fmt.Errorf("invalid think level %q (valid: %s)", v, strings.Join(append([]string{Auto}, Levels...), ", "))
}

func ResolveForWire(level string) (string, error) {
	normalized, ok, err := NormalizeOptional(level)
	if err != nil {
		return "", err
	}
	if !ok || normalized == Auto {
		return "", nil
	}
	return normalized, nil
}

func NormalizeOptional(v string) (string, bool, error) {
	if strings.TrimSpace(v) == "" {
		return "", false, nil
	}
	level, err := Normalize(v)
	return level, true, err
}

func DisplayEnabled(level string) bool {
	normalized, ok, err := NormalizeOptional(level)
	if err != nil || !ok {
		return false
	}
	return normalized != Off && normalized != Auto
}
