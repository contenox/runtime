package contenoxcli

import (
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentidentity"
	"github.com/contenox/runtime/runtime/localtools"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

func agentGitIdentity(db libdb.DBManager) localtools.GitOption {
	return localtools.WithGitIdentity(agentidentity.DefaultKeySource().Resolver(runtimetypes.New(db.WithoutTransaction())))
}
