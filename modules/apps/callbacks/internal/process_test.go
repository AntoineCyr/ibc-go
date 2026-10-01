package internal

import (
	"errors"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store/metrics"
	"cosmossdk.io/store/rootmulti"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/ibc-go/v10/modules/apps/callbacks/types"
	"github.com/stretchr/testify/require"
)

type owner struct{ adopted, aborted bool }

func (o *owner) PrepareAdopt() error { return nil }
func (o *owner) Adopt()              { o.adopted = true }
func (o *owner) Abort() {
	if !o.adopted {
		o.aborted = true
	}
}

type fatalInvariant struct{}

func (fatalInvariant) FatalExecution() {}

func TestExternalCallbackOwnershipAndFatalDisposition(t *testing.T) {
	for _, mode := range []string{"success", "error", "panic", "fatal"} {
		t.Run(mode, func(t *testing.T) {
			db := dbm.NewMemDB()
			defer db.Close()
			ms := rootmulti.NewStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
			key := storetypes.NewKVStoreKey("callback")
			ms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, nil)
			require.NoError(t, ms.LoadLatestVersion())
			ctx := sdk.NewContext(ms.CacheMultiStore(), cmtproto.Header{}, false, log.NewNopLogger()).WithGasMeter(storetypes.NewGasMeter(100_000))
			scope := new(owner)
			ctx = sdk.WithCacheScopeFactory(ctx, func(parent, child sdk.Context) (sdk.Context, sdk.CacheScope, error) { return child, scope, nil })
			execute := func() error {
				return ProcessCallback(ctx, types.CallbackTypeAcknowledgementPacket, types.CallbackData{ExecutionGasLimit: 10_000, CommitGasLimit: 10_000}, func(child sdk.Context) error {
					child.KVStore(key).Set([]byte("write"), []byte("native-equivalent"))
					switch mode {
					case "error":
						return errors.New("callback rejected")
					case "panic":
						panic("callback failed")
					case "fatal":
						panic(fatalInvariant{})
					}
					return nil
				})
			}
			if mode == "fatal" {
				require.PanicsWithValue(t, fatalInvariant{}, func() { _ = execute() })
			} else if mode == "success" {
				require.NoError(t, execute())
			} else {
				require.Error(t, execute())
			}
			if mode == "success" {
				require.True(t, scope.adopted)
				require.Equal(t, []byte("native-equivalent"), ctx.KVStore(key).Get([]byte("write")))
			} else {
				require.True(t, scope.aborted)
				require.Nil(t, ctx.KVStore(key).Get([]byte("write")))
			}
		})
	}
}
