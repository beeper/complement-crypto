package langs

import (
	"fmt"

	"github.com/matrix-org/complement-crypto/internal/api"
	"github.com/matrix-org/complement-crypto/internal/api/mautrixgo"
	"github.com/matrix-org/complement/ct"
)

func init() {
	fmt.Println("Adding mautrix-go bindings")
	SetLanguageBinding(api.ClientTypeMautrixGo, &MautrixGoBindings{})
}

type MautrixGoBindings struct{}

var _ api.LanguageBindings = (*MautrixGoBindings)(nil)

func (b *MautrixGoBindings) PreTestRun(contextID string) {
}

func (b *MautrixGoBindings) PostTestRun(contextID string) {
}

func (b *MautrixGoBindings) MustCreateClient(t ct.TestLike, cfg api.ClientCreationOpts) api.Client {
	client, err := mautrixgo.NewMautrixClient(t, cfg)
	if err != nil {
		ct.Fatalf(t, "Failed to create mautrix-go client: %s", err)
	}
	return client
}
