package agentidentity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/contenox/runtime/runtime/internal/winproc"
)

type Person struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Source string `json:"source"`
}

const (
	PersonFromOverride  = "override"
	PersonFromGit       = "git"
	PersonFromGitConfig = "gitconfig"
)

func (p Person) String() string { return fmt.Sprintf("%s <%s>", p.Name, p.Email) }

var errNoPerson = errors.New("no user.name and user.email are set in git")

var gitConfigGet = func(ctx context.Context, dir string, args ...string) (string, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	full := make([]string, 0, len(args)+3)
	if dir != "" {
		full = append(full, "-C", dir)
	}
	full = append(full, "config")
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, path, full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	winproc.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func ParsePerson(raw string) (Person, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil || strings.TrimSpace(addr.Name) == "" {
		return Person{}, fmt.Errorf("%q is not in the form Name <email>", raw)
	}
	return Person{Name: strings.TrimSpace(addr.Name), Email: addr.Address, Source: PersonFromOverride}, nil
}

func gitPerson(ctx context.Context, dir string, scope ...string) (Person, bool) {
	name, err := gitConfigGet(ctx, dir, append(append([]string{}, scope...), "--get", "user.name")...)
	if err != nil || name == "" {
		return Person{}, false
	}
	email, err := gitConfigGet(ctx, dir, append(append([]string{}, scope...), "--get", "user.email")...)
	if err != nil || email == "" {
		return Person{}, false
	}
	return Person{Name: name, Email: email, Source: PersonFromGit}, true
}

func DefaultPerson(ctx context.Context) (Person, error) {
	if p, ok := gitPerson(ctx, "", "--global", "--includes"); ok {
		return p, nil
	}
	sig, err := UserGitIdentity()
	if err != nil {
		return Person{}, errNoPerson
	}
	return Person{Name: sig.Name, Email: sig.Email, Source: PersonFromGitConfig}, nil
}

func RepositoryPerson(ctx context.Context, repoDir string) (Person, error) {
	if repoDir != "" {
		if p, ok := gitPerson(ctx, repoDir); ok {
			return p, nil
		}
	}
	return DefaultPerson(ctx)
}

func (i Identity) Credit(ctx context.Context, repoDir string) (Person, bool) {
	if !i.CreditMe {
		return Person{}, false
	}
	var person Person
	if override := strings.TrimSpace(i.CreditAs); override != "" {
		p, err := ParsePerson(override)
		if err != nil {
			return Person{}, false
		}
		person = p
	} else {
		p, err := RepositoryPerson(ctx, repoDir)
		if err != nil {
			return Person{}, false
		}
		person = p
	}
	if strings.EqualFold(person.Email, strings.TrimSpace(i.Email)) {
		return Person{}, false
	}
	return person, true
}
