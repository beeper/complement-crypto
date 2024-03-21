package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement-crypto/internal/api"
	"github.com/matrix-org/complement-crypto/internal/api/langs"
	"github.com/matrix-org/complement-crypto/internal/config"
	"github.com/matrix-org/complement-crypto/internal/deploy"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/matrix-org/complement/must"
)

// globals to ensure we are always referring to the same set of HSes/proxies between tests
var (
	ssDeployment           *deploy.SlidingSyncDeployment
	ssMutex                *sync.Mutex
	complementCryptoConfig *config.ComplementCrypto // set in TestMain
)

// Main entry point when users run `go test`. Defined in https://pkg.go.dev/testing#hdr-Main
func TestMain(m *testing.M) {
	complementCryptoConfig = config.NewComplementCryptoConfigFromEnvVars()
	ssMutex = &sync.Mutex{}

	for _, binding := range complementCryptoConfig.Bindings() {
		binding.PreTestRun()
	}

	complement.TestMainWithCleanup(m, "crypto", func() { // always teardown even if panicking
		ssMutex.Lock()
		if ssDeployment != nil {
			ssDeployment.Teardown()
		}
		ssMutex.Unlock()
		for _, binding := range complementCryptoConfig.Bindings() {
			binding.PostTestRun()
		}
	})
}

// Deploy a new network of HSes. If Deploy has been called before, returns the existing
// deployment.
func Deploy(t *testing.T) *deploy.SlidingSyncDeployment {
	ssMutex.Lock()
	defer ssMutex.Unlock()
	if ssDeployment != nil {
		return ssDeployment
	}
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to find working directory: %s", err)
	}
	mitmProxyAddonsDir := filepath.Join(workingDir, "mitmproxy_addons")
	ssDeployment = deploy.RunNewDeployment(t, mitmProxyAddonsDir, complementCryptoConfig.TCPDump)
	return ssDeployment
}

// ClientTypeMatrix enumerates all provided client permutations given by the test client
// matrix `COMPLEMENT_CRYPTO_TEST_CLIENT_MATRIX`. Creates sub-tests for each permutation
// and invokes `subTest`. Sub-tests are run in series.
func ClientTypeMatrix(t *testing.T, subTest func(t *testing.T, clientTypeA, clientTypeB api.ClientType)) {
	for _, tc := range complementCryptoConfig.TestClientMatrix {
		tc := tc
		t.Run(fmt.Sprintf("%s|%s", tc[0], tc[1]), func(t *testing.T) {
			subTest(t, tc[0], tc[1])
		})
	}
}

// ForEachClientType enumerates all known client implementations and creates sub-tests for
// each. Sub-tests are run in series. Always defaults to `hs1`.
func ForEachClientType(t *testing.T, subTest func(t *testing.T, clientType api.ClientType)) {
	for _, tc := range []api.ClientType{{Lang: api.ClientTypeRust, HS: "hs1"}, {Lang: api.ClientTypeJS, HS: "hs1"}} {
		tc := tc
		if !complementCryptoConfig.ShouldTest(tc.Lang) {
			continue
		}
		t.Run(string(tc.Lang), func(t *testing.T) {
			subTest(t, tc)
		})
	}
}

// MustCreateClient creates an api.Client with the specified language/server, else fails the test.
//
// Options can be provided to configure clients, such as enabling persistent storage.
func MustCreateClient(t *testing.T, clientType api.ClientType, cfg api.ClientCreationOpts, opts ...func(api.Client, *api.ClientCreationOpts)) api.Client {
	bindings := langs.GetLanguageBindings(clientType.Lang)
	if bindings == nil {
		t.Fatalf("unknown language: %s", clientType.Lang)
	}
	c := bindings.MustCreateClient(t, cfg)
	for _, o := range opts {
		o(c, &cfg)
	}
	return c
}

// WithDoLogin is an option which can be provided to MustCreateClient which will automatically login, else fail the test.
func WithDoLogin(t *testing.T) func(api.Client, *api.ClientCreationOpts) {
	return func(c api.Client, opts *api.ClientCreationOpts) {
		must.NotError(t, "failed to login", c.Login(t, *opts))
	}
}

// WithPersistentStorage is an option which can be provided to MustCreateClient which will configure clients to use persistent storage,
// e.g IndexedDB or sqlite3 files.
func WithPersistentStorage() func(*api.ClientCreationOpts) {
	return func(o *api.ClientCreationOpts) {
		o.PersistentStorage = true
	}
}

// TestContext provides a consistent set of variables which most tests will need access to.
type TestContext struct {
	Deployment *deploy.SlidingSyncDeployment
	// Alice is defined if at least 1 clientType is provided to CreateTestContext.
	Alice           *client.CSAPI
	AliceClientType api.ClientType
	// Bob is defined if at least 2 clientTypes are provided to CreateTestContext.
	Bob           *client.CSAPI
	BobClientType api.ClientType
	// Charlie is defined if at least 3 clientTypes are provided to CreateTestContext.
	Charlie           *client.CSAPI
	CharlieClientType api.ClientType
}

// CreateTestContext creates a new test context suitable for immediate use. The variadic clientTypes
// control how many clients are automatically registered:
//   - 1x clientType = Alice
//   - 2x clientType = Alice, Bob
//   - 3x clientType = Alice, Bob, Charlie
//
// You can then either login individual users using testContext.MustLoginClient or use the helper functions
// testContext.WithAliceAndBobSyncing which will automatically create js/rust clients and start sync loops
// for you, along with handling cleanup.
func CreateTestContext(t *testing.T, clientType ...api.ClientType) *TestContext {
	deployment := Deploy(t)
	tc := &TestContext{
		Deployment: deployment,
	}
	// pre-register alice and bob, if told
	if len(clientType) > 0 {
		tc.Alice = deployment.Register(t, clientType[0].HS, helpers.RegistrationOpts{
			LocalpartSuffix: "alice",
			Password:        "complement-crypto-password",
		})
		tc.AliceClientType = clientType[0]
	}
	if len(clientType) > 1 {
		tc.Bob = deployment.Register(t, clientType[1].HS, helpers.RegistrationOpts{
			LocalpartSuffix: "bob",
			Password:        "complement-crypto-password",
		})
		tc.BobClientType = clientType[1]
	}
	if len(clientType) > 2 {
		tc.Charlie = deployment.Register(t, clientType[2].HS, helpers.RegistrationOpts{
			LocalpartSuffix: "charlie",
			Password:        "complement-crypto-password",
		})
		tc.CharlieClientType = clientType[2]
	}
	if len(clientType) > 3 {
		t.Fatalf("CreateTestContext: too many clients: got %d", len(clientType))
	}
	return tc
}

func (c *TestContext) WithClientSyncing(t *testing.T, clientType api.ClientType, cli *client.CSAPI, callback func(cli api.Client)) {
	t.Helper()
	clientUnderTest := c.MustLoginClient(t, cli, clientType)
	defer clientUnderTest.Close(t)
	stopSyncing := clientUnderTest.MustStartSyncing(t)
	defer stopSyncing()
	callback(clientUnderTest)
}

// WithAliceSyncing is a helper function which creates a rust/js client and automatically logs in Alice and starts
// a sync loop for her.
//
// The callback function is invoked after this, and cleanup functions are called on your behalf when the
// callback function ends.
func (c *TestContext) WithAliceSyncing(t *testing.T, callback func(alice api.Client)) {
	t.Helper()
	must.NotEqual(t, c.Alice, nil, "No Alice defined. Call CreateTestContext() with at least 1 api.ClientType.")
	c.WithClientSyncing(t, c.AliceClientType, c.Alice, callback)
}

// WithAliceAndBobSyncing is a helper function which creates rust/js clients and automatically logs in Alice & Bob
// and starts a sync loop for both.
//
// The callback function is invoked after this, and cleanup functions are called on your behalf when the
// callback function ends.
func (c *TestContext) WithAliceAndBobSyncing(t *testing.T, callback func(alice, bob api.Client)) {
	t.Helper()
	must.NotEqual(t, c.Bob, nil, "No Bob defined. Call CreateTestContext() with at least 2 api.ClientTypes.")
	// we invert the order here because _typically_ Alice encrypts for Bob, so we want to make sure Bob has
	// uploaded device keys before Alice starts syncing.
	c.WithClientSyncing(t, c.BobClientType, c.Bob, func(bob api.Client) {
		t.Helper()
		c.WithClientSyncing(t, c.AliceClientType, c.Alice, func(alice api.Client) {
			t.Helper()
			callback(alice, bob)
		})
	})
}

// WithAliceBobAndCharlieSyncing is a helper function which creates rust/js clients and automatically logs in Alice, Bob
// and Charlie and starts a sync loop for all.
//
// The callback function is invoked after this, and cleanup functions are called on your behalf when the
// callback function ends.
func (c *TestContext) WithAliceBobAndCharlieSyncing(t *testing.T, callback func(alice, bob, charlie api.Client)) {
	t.Helper()
	must.NotEqual(t, c.Charlie, nil, "No Charlie defined. Call CreateTestContext() with at least 3 api.ClientTypes.")
	// we invert the order here because _typically_ Alice encrypts for Bob & Charlie, so we want to make sure they have
	// uploaded device keys before Alice starts syncing.
	c.WithClientSyncing(t, c.CharlieClientType, c.Charlie, func(charlie api.Client) {
		t.Helper()
		c.WithClientSyncing(t, c.BobClientType, c.Bob, func(bob api.Client) {
			t.Helper()
			c.WithClientSyncing(t, c.AliceClientType, c.Alice, func(alice api.Client) {
				t.Helper()
				callback(alice, bob, charlie)
			})
		})
	})
}

// An option to customise the behaviour of CreateNewEncryptedRoom
type EncRoomOption = func(reqBody map[string]interface{})

// CreateNewEncryptedRoom calls creator.MustCreateRoom with the correct m.room.encryption state event.
//
// options is a set of EncRoomOption that may be provided using methods on
// EncRoomOptions:
// - Preset*: the preset argument passed to createRoom (default: "private_chat")
// - Invite: a list of usernames to invite to the room (default: empty list)
// - RotationPeriodMsgs: value of the rotation_period_msgs param (default: omitted)
func (c *TestContext) CreateNewEncryptedRoom(
	t *testing.T,
	creator *client.CSAPI,
	options ...EncRoomOption,
) (roomID string) {
	t.Helper()

	reqBody := map[string]interface{}{
		"name":   t.Name(),
		"preset": "private_chat",
		"invite": []string{},
		"initial_state": []map[string]interface{}{
			{
				"type":      "m.room.encryption",
				"state_key": "",
				"content": map[string]interface{}{
					"algorithm": "m.megolm.v1.aes-sha2",
				},
			},
		},
	}

	for _, option := range options {
		option(reqBody)
	}

	return creator.MustCreateRoom(t, reqBody)
}

type encRoomOptions int

// A namespace for the various options that may be passed in to CreateNewEncryptedRoom
const EncRoomOptions encRoomOptions = 0

// An option for CreateNewEncryptedRoom that requests the `preset` field to be
// set to `private_chat`.
func (encRoomOptions) PresetPrivateChat() EncRoomOption {
	return setPreset("private_chat")
}

// An option for CreateNewEncryptedRoom that requests the `preset` field to be
// set to `trusted_private_chat`.
func (encRoomOptions) PresetTrustedPrivateChat() EncRoomOption {
	return setPreset("trusted_private_chat")
}

// An option for CreateNewEncryptedRoom that requests the `preset` field to be
// set to `public_chat`.
func (encRoomOptions) PresetPublicChat() EncRoomOption {
	return setPreset("public_chat")
}

func setPreset(preset string) EncRoomOption {
	return func(reqBody map[string]interface{}) {
		reqBody["preset"] = preset
	}
}

// An option for CreateNewEncryptedRoom that provides a list of Matrix usernames
// to be supplied in the `invite` field.
func (encRoomOptions) Invite(invite []string) EncRoomOption {
	return func(reqBody map[string]interface{}) {
		reqBody["invite"] = invite
	}
}

// An option for CreateNewEncryptedRoom that adds a `rotation_period_msgs` field
// to the `m.room.encryption` event supplied when the room is created.
func (encRoomOptions) RotationPeriodMsgs(numMsgs int) EncRoomOption {
	return func(reqBody map[string]interface{}) {
		var initial_state = reqBody["initial_state"].([]map[string]interface{})
		var event = initial_state[0]
		var content = event["content"].(map[string]interface{})
		content["rotation_period_msgs"] = numMsgs
	}
}

// OptsFromClient converts a Complement client into a set of options which can be used to create an api.Client.
func (c *TestContext) OptsFromClient(t *testing.T, existing *client.CSAPI, options ...func(*api.ClientCreationOpts)) api.ClientCreationOpts {
	o := &api.ClientCreationOpts{
		BaseURL:  existing.BaseURL,
		UserID:   existing.UserID,
		DeviceID: existing.DeviceID,
		Password: existing.Password,
	}
	for _, opt := range options {
		opt(o)
	}
	return *o
}

// MustRegisterNewDevice logs in a new device for this client, else fails the test.
func (c *TestContext) MustRegisterNewDevice(t *testing.T, cli *client.CSAPI, hsName, newDeviceID string) *client.CSAPI {
	return c.Deployment.Login(t, hsName, cli, helpers.LoginOpts{
		DeviceID: newDeviceID,
		Password: cli.Password,
	})
}

// MustCreateClient creates an api.Client from an existing Complement client and the specified client type. Additional options
// can be set to configure the client beyond that of the Complement client e.g to add persistent storage.
func (c *TestContext) MustCreateClient(t *testing.T, cli *client.CSAPI, clientType api.ClientType, options ...func(*api.ClientCreationOpts)) api.Client {
	t.Helper()
	cfg := api.NewClientCreationOpts(cli)
	for _, opt := range options {
		opt(&cfg)
	}
	cfg.SlidingSyncURL = c.Deployment.SlidingSyncURLForHS(t, clientType.HS)
	client := MustCreateClient(t, clientType, cfg)
	return client
}

// MustLoginClient is the same as MustCreateClient but also logs in the client. TODO REMOVE
func (c *TestContext) MustLoginClient(t *testing.T, cli *client.CSAPI, clientType api.ClientType, options ...func(*api.ClientCreationOpts)) api.Client {
	t.Helper()
	client := c.MustCreateClient(t, cli, clientType, options...)
	must.NotError(t, "failed to login client", client.Login(t, client.Opts()))
	return client
}
