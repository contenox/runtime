package libevents

import (
	"fmt"
	"regexp"
)

type Config struct {
	TablePrefix string

	ScopeColumn string
}

var identRx = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (c Config) Validate() error {
	if !identRx.MatchString(c.TablePrefix) {
		return fmt.Errorf("libevents: invalid table prefix %q", c.TablePrefix)
	}
	if !identRx.MatchString(c.ScopeColumn) {
		return fmt.Errorf("libevents: invalid scope column %q", c.ScopeColumn)
	}
	return nil
}

func (c Config) table(name string) string { return c.TablePrefix + name }
