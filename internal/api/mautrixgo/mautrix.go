package mautrixgo

import (
	"context"
	"database/sql"
	"os"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/id"

	"github.com/matrix-org/complement-crypto/internal/api"
	"github.com/matrix-org/complement/ct"
)

type MautrixClient struct {
	client       *mautrix.Client
	opts         api.ClientCreationOpts
	cryptoHelper *cryptohelper.CryptoHelper
}

var _ api.Client = &MautrixClient{}

func NewMautrixClient(t ct.TestLike, opts api.ClientCreationOpts) (*MautrixClient, error) {
	client, err := mautrix.NewClient(opts.BaseURL, "", "")
	if err != nil {
		return nil, err
	}

	var rawDB *sql.DB
	if opts.PersistentStorage {
		rawDB, err = sql.Open("sqlite3", "file:mautrix-go.db?_txlock=immediate")
	} else {
		rawDB, err = sql.Open("sqlite3", ":memory:")
	}
	if err != nil {
		return nil, err
	}

	db, err := dbutil.NewWithDB(rawDB, "sqlite3")
	if err != nil {
		return nil, err
	}

	cryptoHelper, err := cryptohelper.NewCryptoHelper(client, []byte("mautrix-complement-crypto"), db)
	if err != nil {
		return nil, err
	}
	client.Crypto = cryptoHelper

	return &MautrixClient{client: client, opts: opts, cryptoHelper: cryptoHelper}, nil
}

func (c *MautrixClient) Close(t ct.TestLike) {
	c.cryptoHelper.Close()
}
func (c *MautrixClient) ForceClose(t ct.TestLike) {
	c.Close(t)
}

func (c *MautrixClient) DeletePersistentStorage(t ct.TestLike) {
	os.Remove("mautrix-go.db")
}

func (c *MautrixClient) Login(t ct.TestLike, opts api.ClientCreationOpts) error {
	t.Logf("Logging in as %s", opts.UserID)
	c.cryptoHelper.LoginAs = &mautrix.ReqLogin{
		Type:       mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: opts.UserID},
		Password:   opts.Password,
	}
	c.cryptoHelper.DBAccountID = opts.UserID
	return c.cryptoHelper.Init(context.TODO())
}

func (c *MautrixClient) MustStartSyncing(t ct.TestLike) (stopSyncing func()) {
	stopSyncing, err := c.StartSyncing(t)
	if err != nil {
		t.Fatalf("Failed to start syncing: %s", err)
	}
	return stopSyncing
}

func (c *MautrixClient) StartSyncing(t ct.TestLike) (stopSyncing func(), err error) {
	panic("start syncing")
}

func (c *MautrixClient) IsRoomEncrypted(t ct.TestLike, roomID string) (bool, error) {
	return c.client.StateStore.IsEncrypted(context.TODO(), id.RoomID(roomID))
}

func (c *MautrixClient) SendMessage(t ct.TestLike, roomID, text string) (eventID string) {
	eventID, err := c.TrySendMessage(t, roomID, text)
	if err != nil {
		t.Fatalf("Failed to send message: %s", err)
	}
	return eventID
}

func (c *MautrixClient) TrySendMessage(t ct.TestLike, roomID, text string) (eventID string, err error) {
	resp, err := c.client.SendText(context.TODO(), id.RoomID(roomID), text)
	if err != nil {
		return "", err
	}
	return resp.EventID.String(), nil
}

func (c *MautrixClient) WaitUntilEventInRoom(t ct.TestLike, roomID string, checker func(e api.Event) bool) api.Waiter {
	panic("implement me")
}

func (c *MautrixClient) MustBackpaginate(t ct.TestLike, roomID string, count int) {
	panic("implement me")
}

func (c *MautrixClient) MustGetEvent(t ct.TestLike, roomID, eventID string) api.Event {
	panic("implement me")
}

func (c *MautrixClient) MustBackupKeys(t ct.TestLike) (recoveryKey string) {
	panic("implement me")
}

func (c *MautrixClient) MustLoadBackup(t ct.TestLike, recoveryKey string) {
	panic("implement me")
}

func (c *MautrixClient) LoadBackup(t ct.TestLike, recoveryKey string) error {
	panic("implement me")
}

func (c *MautrixClient) GetNotification(t ct.TestLike, roomID, eventID string) (*api.Notification, error) {
	panic("implement me")
}

func (c *MautrixClient) Logf(t ct.TestLike, format string, args ...interface{}) {
	panic("implement me")
}

func (c *MautrixClient) UserID() string {
	return c.client.UserID.String()
}

func (c *MautrixClient) CurrentAccessToken(t ct.TestLike) string {
	return c.client.AccessToken
}

func (c *MautrixClient) Type() api.ClientTypeLang {
	return api.ClientTypeMautrixGo
}

func (c *MautrixClient) Opts() api.ClientCreationOpts {
	return c.opts
}
