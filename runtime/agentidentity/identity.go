package agentidentity

import (
	"context"
	"fmt"
	"strings"

	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const (
	ConfigName     = "agent-git-name"
	ConfigEmail    = "agent-git-email"
	ConfigKey      = "agent-git-key"
	ConfigSign     = "agent-git-sign"
	ConfigCreditMe = "agent-git-credit-me"
	ConfigCreditAs = "agent-git-credit-as"
)

type Identity struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Key      string `json:"key"`
	Sign     bool   `json:"sign"`
	CreditMe bool   `json:"creditMe"`
	CreditAs string `json:"creditAs"`
}

func (i Identity) Active() bool {
	return strings.TrimSpace(i.Name) != "" && strings.TrimSpace(i.Email) != ""
}

func (i Identity) Normalized() Identity {
	i.Name = strings.TrimSpace(i.Name)
	i.Email = strings.TrimSpace(i.Email)
	i.Key = strings.TrimSpace(i.Key)
	i.CreditAs = strings.TrimSpace(i.CreditAs)
	return i
}

func (i Identity) Validate() error {
	i = i.Normalized()
	if (i.Name == "") != (i.Email == "") {
		return fmt.Errorf("agent identity needs both a name and an email, or neither")
	}
	if i.Email != "" && (!strings.Contains(i.Email, "@") || strings.ContainsAny(i.Email, " <>")) {
		return fmt.Errorf("agent identity email %q is not an email address", i.Email)
	}
	if strings.ContainsAny(i.Name, "<>\n") {
		return fmt.Errorf("agent identity name may not contain <, > or a line break")
	}
	if i.CreditAs != "" {
		if _, err := ParsePerson(i.CreditAs); err != nil {
			return fmt.Errorf("credit as: %w", err)
		}
	}
	if i.Sign && i.Key == "" {
		return fmt.Errorf("signing commits needs an SSH key; choose or generate one first")
	}
	return nil
}

func Load(ctx context.Context, store runtimetypes.Store) Identity {
	return Identity{
		Name:     clikv.Read(ctx, store, ConfigName),
		Email:    clikv.Read(ctx, store, ConfigEmail),
		Key:      clikv.Read(ctx, store, ConfigKey),
		Sign:     clikv.Read(ctx, store, ConfigSign) == "on",
		CreditMe: clikv.Read(ctx, store, ConfigCreditMe) != "off",
		CreditAs: clikv.Read(ctx, store, ConfigCreditAs),
	}.Normalized()
}

func Save(ctx context.Context, store runtimetypes.Store, identity Identity) error {
	identity = identity.Normalized()
	if err := identity.Validate(); err != nil {
		return err
	}
	for key, value := range map[string]string{
		ConfigName:     identity.Name,
		ConfigEmail:    identity.Email,
		ConfigKey:      identity.Key,
		ConfigSign:     onOff(identity.Sign),
		ConfigCreditMe: onOff(identity.CreditMe),
		ConfigCreditAs: identity.CreditAs,
	} {
		if err := clikv.SetString(ctx, store, key, value); err != nil {
			return fmt.Errorf("save %s: %w", key, err)
		}
	}
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
