package tests_test

import (
	"testing"
	"time"

	test_helpers "github.com/terra-money/alliance/app"
	"github.com/terra-money/alliance/x/alliance/keeper"
	"github.com/terra-money/alliance/x/alliance/types"

	sdkmath "cosmossdk.io/math"
	abcitypes "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	teststaking "github.com/cosmos/cosmos-sdk/x/staking/testutil"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"
)

func TestRewardPoolAndGlobalIndex(t *testing.T) {
	app, ctx := createTestContext(t)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			{
				Denom:             AllianceDenom,
				RewardWeight:      sdkmath.LegacyNewDec(2),
				RewardWeightRange: types.RewardWeightRange{Min: sdkmath.LegacyZeroDec(), Max: sdkmath.LegacyNewDec(5)},
				TakeRate:          sdkmath.LegacyNewDec(0),
				TotalTokens:       sdkmath.ZeroInt(),
			},
			{
				Denom:             AllianceDenomTwo,
				RewardWeight:      sdkmath.LegacyNewDec(10),
				RewardWeightRange: types.RewardWeightRange{Min: sdkmath.LegacyNewDec(5), Max: sdkmath.LegacyNewDec(15)},
				TakeRate:          sdkmath.LegacyNewDec(0),
				TotalTokens:       sdkmath.ZeroInt(),
			},
		},
	})

	// Accounts
	rewardsPoolAddr := app.AccountKeeper.GetModuleAddress(types.RewardsPoolName)
	mintPoolAddr := app.AccountKeeper.GetModuleAddress(minttypes.ModuleName)
	delegations, err := app.StakingKeeper.GetAllDelegations(ctx)
	require.NoError(t, err)
	valAddr1, err := sdk.ValAddressFromBech32(delegations[0].ValidatorAddress)
	require.NoError(t, err)
	val1, err := app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	require.NoError(t, err)
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 2, sdk.NewCoins(
		sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)),
		sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(1000_000)),
	))
	user1 := addrs[0]
	user2 := addrs[1]

	// Mint tokens
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake2", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)
	coin := app.BankKeeper.GetBalance(ctx, mintPoolAddr, "stake")
	require.Equal(t, sdk.NewCoin("stake", sdkmath.NewInt(4000_000)), coin)

	_, err = app.AllianceKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)
	assets := app.AllianceKeeper.GetAllAssets(ctx)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(2000_000))))
	require.NoError(t, err)

	// Expect rewards pool to have something
	balance := app.BankKeeper.GetBalance(ctx, rewardsPoolAddr, "stake")
	require.Equal(t, sdk.NewCoin("stake", sdkmath.NewInt(2000_000)), balance)

	// Expect validator global index to be updated
	require.NoError(t, err)
	globalIndices := types.NewRewardHistories(val1.GlobalRewardHistory)
	require.Equal(t, types.RewardHistories{
		types.RewardHistory{
			Denom: "stake",
			Index: sdkmath.LegacyNewDec(1),
		},
	}, globalIndices)

	// New delegation from user 2
	_, err = app.AllianceKeeper.Delegate(ctx, user2, val1, sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)
	assets = app.AllianceKeeper.GetAllAssets(ctx)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(2000_000))))
	require.NoError(t, err)

	globalIndices = types.NewRewardHistories(val1.GlobalRewardHistory)
	require.Equal(t, types.RewardHistories{
		types.RewardHistory{
			Denom: "stake",
			Index: sdkmath.LegacyNewDec(14).Quo(sdkmath.LegacyNewDec(12)),
		},
	}, globalIndices)

	// Transfer another token to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake2", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)

	// Expect global index to be updated
	// 14/12 + 4/12 = 18/12
	globalIndices = types.NewRewardHistories(val1.GlobalRewardHistory)
	require.Equal(t, types.RewardHistories{
		types.RewardHistory{
			Denom: "stake",
			Index: sdkmath.LegacyNewDec(14).Quo(sdkmath.LegacyNewDec(12)),
		},
		types.RewardHistory{
			Denom: "stake2",
			Index: sdkmath.LegacyNewDec(4).Quo(sdkmath.LegacyNewDec(12)),
		},
	}, globalIndices)
}

func TestClaimRewards(t *testing.T) {
	app, ctx := createTestContext(t)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			types.NewAllianceAsset(AllianceDenom, sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(0), sdkmath.LegacyNewDec(5), sdkmath.LegacyNewDec(0), ctx.BlockTime()),
			types.NewAllianceAsset(AllianceDenomTwo, sdkmath.LegacyNewDec(10), sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(12), sdkmath.LegacyNewDec(0), ctx.BlockTime()),
		},
	})

	// Accounts
	mintPoolAddr := app.AccountKeeper.GetModuleAddress(minttypes.ModuleName)
	rewardsPoolAddr := app.AccountKeeper.GetModuleAddress(types.RewardsPoolName)
	delegations, err := app.StakingKeeper.GetAllDelegations(ctx)
	require.NoError(t, err)
	valAddr1, err := sdk.ValAddressFromBech32(delegations[0].ValidatorAddress)
	require.NoError(t, err)
	val1, err := app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	require.NoError(t, err)
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 2, sdk.NewCoins(
		sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)),
		sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(1000_000)),
	))
	user1 := addrs[0]
	user2 := addrs[1]

	// Mint tokens
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake2", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)

	// New delegation from user 1
	_, err = app.AllianceKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)
	assets := app.AllianceKeeper.GetAllAssets(ctx)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(2000_000))))
	require.NoError(t, err)

	// New delegation from user 2
	_, err = app.AllianceKeeper.Delegate(ctx, user2, val1, sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)
	assets = app.AllianceKeeper.GetAllAssets(ctx)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(2000_000))))
	require.NoError(t, err)

	asset, _ := app.AllianceKeeper.GetAssetByDenom(ctx, AllianceDenom)
	require.Equal(t,
		sdkmath.NewInt(1000_000),
		val1.TotalTokensWithAsset(asset).TruncateInt(),
	)
	asset, _ = app.AllianceKeeper.GetAssetByDenom(ctx, AllianceDenomTwo)
	require.Equal(t,
		sdkmath.NewInt(1000_000),
		val1.TotalTokensWithAsset(asset).TruncateInt(),
	)

	// Transfer another token to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake2", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)

	// Make sure reward indices are right
	require.Equal(t,
		types.NewRewardHistories([]types.RewardHistory{
			{
				Denom: "stake",
				Index: sdkmath.LegacyMustNewDecFromStr("1.166666666666666667"),
			},
			{
				Denom: "stake2",
				Index: sdkmath.LegacyMustNewDecFromStr("0.333333333333333333"),
			},
		}),
		types.NewRewardHistories(val1.GlobalRewardHistory),
	)

	// before claiming, there should be tokens in rewards pool
	coins := app.BankKeeper.GetAllBalances(ctx, rewardsPoolAddr)
	require.Equal(t,
		sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(4000_000)), sdk.NewCoin("stake2", sdkmath.NewInt(4000_000))),
		coins,
	)

	// User 1 claims rewards
	// User 1 has 1 STAKE (2 Power)
	// Added 2 stake rewards (fully belonging to user 1)
	// User 2 has 1 STAKE (10 Power)
	// Added 2 stake rewards (user1: 2/12 * 2, user2: 10/12 * 2)
	// Added 4 stake2 rewards (user1: 2/12 * 4, user2: 10/12 * 4)
	coins, err = app.AllianceKeeper.ClaimDelegationRewards(ctx, user1, val1, AllianceDenom)
	require.NoError(t, err)
	require.Equal(t, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(2_333_333)), sdk.NewCoin("stake2", sdkmath.NewInt(666_666))), coins)

	// User 2 claims rewards but doesn't use the right denom
	_, err = app.AllianceKeeper.ClaimDelegationRewards(ctx, user2, val1, AllianceDenom)
	require.Error(t, err)

	// User 2 claims rewards
	coins, err = app.AllianceKeeper.ClaimDelegationRewards(ctx, user2, val1, AllianceDenomTwo)
	require.NoError(t, err)
	require.Equal(t, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(1_666_666)), sdk.NewCoin("stake2", sdkmath.NewInt(3_333_333))), coins)

	// After claiming, there should be nothing left in rewards pool
	// Some rounding left
	coins = app.BankKeeper.GetAllBalances(ctx, rewardsPoolAddr)
	require.Equal(t, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(1)), sdk.NewCoin("stake2", sdkmath.NewInt(1))), coins)

	// Global indices
	require.NoError(t, err)
	indices := types.NewRewardHistories(val1.GlobalRewardHistory)

	// Check that all delegations have updated local indices
	delegation, found := app.AllianceKeeper.GetDelegation(ctx, user1, valAddr1, AllianceDenom)
	require.True(t, found)
	require.Equal(t, indices, types.NewRewardHistories(delegation.RewardHistory))

	delegation, found = app.AllianceKeeper.GetDelegation(ctx, user2, valAddr1, AllianceDenomTwo)
	require.True(t, found)
	require.Equal(t, indices, types.NewRewardHistories(delegation.RewardHistory))
}

func TestClaimRewardsBeforeRewardsIssuance(t *testing.T) {
	app, ctx := createTestContext(t)
	ctx = ctx.WithBlockTime(time.Now())
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			types.NewAllianceAsset(AllianceDenom, sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(0), sdkmath.LegacyNewDec(5), sdkmath.LegacyNewDec(0), ctx.BlockTime().Add(-time.Hour)),
			types.NewAllianceAsset(AllianceDenomTwo, sdkmath.LegacyNewDec(10), sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(12), sdkmath.LegacyNewDec(0), ctx.BlockTime().Add(time.Hour)),
		},
	})
	queryServer := keeper.NewQueryServerImpl(app.AllianceKeeper)

	// Set tax and rewards to be zero for easier calculation
	distParams, err := app.DistrKeeper.Params.Get(ctx)
	require.NoError(t, err)
	distParams.CommunityTax = sdkmath.LegacyZeroDec()

	err = app.DistrKeeper.Params.Set(ctx, distParams)
	require.NoError(t, err)

	// Accounts
	mintPoolAddr := app.AccountKeeper.GetModuleAddress(minttypes.ModuleName)
	delegations, err := app.StakingKeeper.GetAllDelegations(ctx)
	require.NoError(t, err)
	valAddr1, err := sdk.ValAddressFromBech32(delegations[0].ValidatorAddress)
	require.NoError(t, err)
	val1, err := app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	require.NoError(t, err)
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 2, sdk.NewCoins(
		sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)),
		sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(1000_000)),
	))
	user1 := addrs[0]
	user2 := addrs[1]

	// Mint tokens
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(6000_000))))
	require.NoError(t, err)
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake2", sdkmath.NewInt(6000_000))))
	require.NoError(t, err)

	// New delegation from user 1
	_, err = app.AllianceKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)
	assets := app.AllianceKeeper.GetAllAssets(ctx)
	app.AllianceKeeper.InitializeAllianceAssets(ctx, assets)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(2000_000))))
	require.NoError(t, err)

	// New delegation from user 2
	_, err = app.AllianceKeeper.Delegate(ctx, user2, val1, sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)
	assets = app.AllianceKeeper.GetAllAssets(ctx)
	app.AllianceKeeper.InitializeAllianceAssets(ctx, assets)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(2000_000))))
	require.NoError(t, err)

	// User 1 claims rewards
	// Should get all the rewards in the pool
	coins, err := app.AllianceKeeper.ClaimDelegationRewards(ctx, user1, val1, AllianceDenom)
	require.NoError(t, err)
	require.Equal(t, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(4_000_000))), coins)

	// SInce user 1 claimed rewards, there should be no tokens in rewards pool
	res, err := queryServer.AllianceDelegationRewards(ctx, &types.QueryAllianceDelegationRewardsRequest{
		DelegatorAddr: user1.String(),
		ValidatorAddr: val1.OperatorAddress,
		Denom:         AllianceDenom,
	})
	require.NoError(t, err)
	require.Equal(t, []sdk.Coin{}, res.Rewards)

	// User 2 shouldn't have staking rewards
	// because RewardStartTime is in the future
	// for the AllianceDenomTwo.
	coins, err = app.AllianceKeeper.ClaimDelegationRewards(ctx, user2, val1, AllianceDenomTwo)
	require.NoError(t, err)
	require.Equal(t, sdk.NewCoins(), coins)

	// Move time forward so alliance 2 is enabled
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(2 * time.Hour))
	assets = app.AllianceKeeper.GetAllAssets(ctx)
	app.AllianceKeeper.InitializeAllianceAssets(ctx, assets)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// User 2 should still not have staking rewards
	// because all reward distributions happened before activation
	coins, err = app.AllianceKeeper.ClaimDelegationRewards(ctx, user2, val1, AllianceDenomTwo)
	require.NoError(t, err)
	require.Len(t, coins, 0)

	// Transfer to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(2000_000))))
	require.NoError(t, err)

	// User 2 should now have rewards
	coins, err = app.AllianceKeeper.ClaimDelegationRewards(ctx, user2, val1, AllianceDenomTwo)
	require.NoError(t, err)
	require.Len(t, coins, 1)
}

func TestClaimRewardsWithMultipleValidators(t *testing.T) {
	var err error
	app, ctx := createTestContext(t)
	startTime := time.Now()
	ctx = ctx.WithBlockTime(startTime)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			types.NewAllianceAsset(AllianceDenom, sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(0), sdkmath.LegacyNewDec(5), sdkmath.LegacyNewDec(0), ctx.BlockTime()),
			types.NewAllianceAsset(AllianceDenomTwo, sdkmath.LegacyNewDec(10), sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(12), sdkmath.LegacyNewDec(0), ctx.BlockTime()),
		},
	})

	// Set tax and rewards to be zero for easier calculation
	distParams, err := app.DistrKeeper.Params.Get(ctx)
	require.NoError(t, err)
	distParams.CommunityTax = sdkmath.LegacyZeroDec()

	err = app.DistrKeeper.Params.Set(ctx, distParams)
	require.NoError(t, err)

	// Accounts
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 4, sdk.NewCoins(
		sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)),
		sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(1000_000)),
	))
	pks := test_helpers.CreateTestPubKeys(2)

	// Creating two validators: 1 with 0% commission, 1 with 100% commission
	valAddr1 := sdk.ValAddress(addrs[0])
	_val1 := teststaking.NewValidator(t, valAddr1, pks[0])
	_val1.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdkmath.LegacyNewDec(0),
			MaxRate:       sdkmath.LegacyNewDec(0),
			MaxChangeRate: sdkmath.LegacyNewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val1)

	valAddr2 := sdk.ValAddress(addrs[1])
	_val2 := teststaking.NewValidator(t, valAddr2, pks[1])
	_val2.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdkmath.LegacyNewDec(1),
			MaxRate:       sdkmath.LegacyNewDec(1),
			MaxChangeRate: sdkmath.LegacyNewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val2)

	user1 := addrs[2]
	user2 := addrs[3]

	// Mint tokens
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake2", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)

	// New delegation from user 1 to val 1
	val1, _ := app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	_, err = app.AllianceKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)

	// New delegation from user 2 to val 2
	val2, _ := app.AllianceKeeper.GetAllianceValidator(ctx, valAddr2)
	_, err = app.AllianceKeeper.Delegate(ctx, user2, val2, sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)

	assets := app.AllianceKeeper.GetAllAssets(ctx)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)
	// Check total bonded amount
	totalBondedTokens, err := app.StakingKeeper.TotalBondedTokens(ctx)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(13_000_000), totalBondedTokens)

	// Transfer to rewards to fee pool to be distributed
	err = app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, authtypes.FeeCollectorName, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)

	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	// Distribute in the next begin block
	// At the next begin block, tokens will be distributed from the fee pool
	cons1, _ := val1.GetConsAddr()
	cons2, _ := val2.GetConsAddr()
	var votingPower int64 = 12
	app.DistrKeeper.AllocateTokens(ctx, votingPower, []abcitypes.VoteInfo{
		{
			Validator: abcitypes.Validator{
				Address: cons1,
				Power:   2,
			},
			BlockIdFlag: cmtproto.BlockIDFlagCommit,
		},
		{
			Validator: abcitypes.Validator{
				Address: cons2,
				Power:   10,
			},
			BlockIdFlag: cmtproto.BlockIDFlagCommit,
		},
	})

	commission, err := app.DistrKeeper.GetValidatorAccumulatedCommission(ctx, []byte(val1.GetOperator()))
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(0), commission.Commission.AmountOf("stake").TruncateInt())
	commission, err = app.DistrKeeper.GetValidatorAccumulatedCommission(ctx, []byte(val2.GetOperator()))
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(3333333), commission.Commission.AmountOf("stake").TruncateInt())

	rewards, err := app.DistrKeeper.GetValidatorCurrentRewards(ctx, []byte(val1.GetOperator()))
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(666666), rewards.Rewards.AmountOf("stake").TruncateInt())
	rewards, err = app.DistrKeeper.GetValidatorCurrentRewards(ctx, []byte(val2.GetOperator()))
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(0), rewards.Rewards.AmountOf("stake").TruncateInt())

	// User 1 should be getting all the rewards from validator 1 since it has 0 commission
	coins, err := app.AllianceKeeper.ClaimDelegationRewards(ctx, user1, val1, AllianceDenom)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(666666), coins.AmountOf("stake"))

	// User 2 should be getting no rewards since validator 2 has 100% commission
	coins, err = app.AllianceKeeper.ClaimDelegationRewards(ctx, user2, val2, AllianceDenomTwo)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(0), coins.AmountOf("stake"))
}

func TestClaimRewardsAfterRewardsRatesChange(t *testing.T) {
	var err error
	app, ctx := createTestContext(t)
	ctx = ctx.WithBlockHeight(1)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			types.NewAllianceAsset(AllianceDenom, sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(0), sdkmath.LegacyNewDec(5), sdkmath.LegacyNewDec(0), ctx.BlockTime()),
			types.NewAllianceAsset(AllianceDenomTwo, sdkmath.LegacyNewDec(10), sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(12), sdkmath.LegacyNewDec(0), ctx.BlockTime()),
		},
	})

	// Set tax and rewards to be zero for easier calculation
	distParams, err := app.DistrKeeper.Params.Get(ctx)
	require.NoError(t, err)
	distParams.CommunityTax = sdkmath.LegacyZeroDec()

	err = app.DistrKeeper.Params.Set(ctx, distParams)
	require.NoError(t, err)

	// Accounts
	bondDenom, err := app.StakingKeeper.BondDenom(ctx)
	require.NoError(t, err)
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 4, sdk.NewCoins(
		sdk.NewCoin(AllianceDenom, sdkmath.NewInt(10_000_000)),
		sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(10_000_000)),
	))

	// Creating two validators: 1 with 0% commission, 1 with 100% commission
	pks := test_helpers.CreateTestPubKeys(2)
	valAddr1 := sdk.ValAddress(addrs[0])
	_val1 := teststaking.NewValidator(t, valAddr1, pks[0])
	_val1.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdkmath.LegacyNewDec(0),
			MaxRate:       sdkmath.LegacyNewDec(0),
			MaxChangeRate: sdkmath.LegacyNewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val1)
	val1, err := app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	require.NoError(t, err)

	valAddr2 := sdk.ValAddress(addrs[1])
	_val2 := teststaking.NewValidator(t, valAddr2, pks[1])
	_val2.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdkmath.LegacyNewDec(0),
			MaxRate:       sdkmath.LegacyNewDec(1),
			MaxChangeRate: sdkmath.LegacyNewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val2)
	val2, err := app.AllianceKeeper.GetAllianceValidator(ctx, valAddr2)
	require.NoError(t, err)

	user1 := addrs[2]
	user2 := addrs[3]

	// New delegations
	_, err = app.AllianceKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)
	_, err = app.AllianceKeeper.Delegate(ctx, user2, val2, sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)

	assets := app.AllianceKeeper.GetAllAssets(ctx)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Accumulate rewards in pool and distribute it
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin(bondDenom, sdkmath.NewInt(40_000_000))))
	require.NoError(t, err)
	err = app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, authtypes.FeeCollectorName, sdk.NewCoins(sdk.NewCoin(bondDenom, sdkmath.NewInt(10_000_000))))
	require.NoError(t, err)

	// Distribute in the next begin block
	// At the next begin block, tokens will be distributed from the fee pool
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	val1, _ = app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	cons1, _ := val1.GetConsAddr()
	power1 := val1.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))

	val2, _ = app.AllianceKeeper.GetAllianceValidator(ctx, valAddr2)
	cons2, _ := val2.GetConsAddr()
	power2 := val2.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))

	app.DistrKeeper.AllocateTokens(ctx, power1+power2, []abcitypes.VoteInfo{
		{
			Validator: abcitypes.Validator{
				Address: cons1,
				Power:   power1,
			},
			BlockIdFlag: cmtproto.BlockIDFlagCommit,
		},
		{
			Validator: abcitypes.Validator{
				Address: cons2,
				Power:   power2,
			},
			BlockIdFlag: cmtproto.BlockIDFlagCommit,
		},
	})

	err = app.AllianceKeeper.UpdateAllianceAsset(ctx, types.NewAllianceAsset(AllianceDenom, sdkmath.LegacyNewDec(10), sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(12), sdkmath.LegacyNewDec(0), ctx.BlockTime()))
	require.NoError(t, err)
	assets = app.AllianceKeeper.GetAllAssets(ctx)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Expect reward change snapshots to be taken
	val1, _ = app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	iter, err := app.AllianceKeeper.IterateWeightChangeSnapshot(ctx, AllianceDenom, valAddr1, 0)
	require.NoError(t, err)
	var snapshot types.RewardWeightChangeSnapshot
	require.True(t, iter.Valid())
	app.AppCodec().MustUnmarshal(iter.Value(), &snapshot)
	require.Equal(t, types.RewardWeightChangeSnapshot{
		PrevRewardWeight: sdkmath.LegacyNewDec(2),
		RewardHistories:  val1.GlobalRewardHistory,
	}, snapshot)
	iter.Close()

	// Accumulate rewards in pool
	err = app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, authtypes.FeeCollectorName, sdk.NewCoins(sdk.NewCoin(bondDenom, sdkmath.NewInt(10_000_000))))
	require.NoError(t, err)

	// Distribute in the next begin block
	// At the next begin block, tokens will be distributed from the fee pool
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	val1, _ = app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	power1 = val1.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))

	val2, _ = app.AllianceKeeper.GetAllianceValidator(ctx, valAddr2)
	power2 = val2.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))
	app.DistrKeeper.AllocateTokens(ctx, power1+power2, []abcitypes.VoteInfo{
		{
			Validator: abcitypes.Validator{
				Address: cons1,
				Power:   power1,
			},
			BlockIdFlag: cmtproto.BlockIDFlagCommit,
		},
		{
			Validator: abcitypes.Validator{
				Address: cons2,
				Power:   power2,
			},
			BlockIdFlag: cmtproto.BlockIDFlagCommit,
		},
	})

	rewards1, err := app.AllianceKeeper.ClaimDelegationRewards(ctx, user1, val1, AllianceDenom)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(5_000_000+1_666_666), rewards1.AmountOf(bondDenom))

	rewards2, err := app.AllianceKeeper.ClaimDelegationRewards(ctx, user2, val2, AllianceDenomTwo)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(5_000_000+8_333_333), rewards2.AmountOf(bondDenom))

	// Accumulate rewards in pool
	err = app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, authtypes.FeeCollectorName, sdk.NewCoins(sdk.NewCoin(bondDenom, sdkmath.NewInt(10_000_000))))
	require.NoError(t, err)

	// Distribute in the next begin block
	// At the next begin block, tokens will be distributed from the fee pool
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	val1, _ = app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	power1 = val1.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))

	val2, _ = app.AllianceKeeper.GetAllianceValidator(ctx, valAddr2)
	power2 = val2.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))
	app.DistrKeeper.AllocateTokens(ctx, power1+power2, []abcitypes.VoteInfo{
		{
			Validator: abcitypes.Validator{
				Address: cons1,
				Power:   power1,
			},
			BlockIdFlag: cmtproto.BlockIDFlagCommit,
		},
		{
			Validator: abcitypes.Validator{
				Address: cons2,
				Power:   power2,
			},
			BlockIdFlag: cmtproto.BlockIDFlagCommit,
		},
	})

	rewards1, err = app.AllianceKeeper.ClaimDelegationRewards(ctx, user1, val1, AllianceDenom)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(5_000_000), rewards1.AmountOf(bondDenom))

	rewards2, err = app.AllianceKeeper.ClaimDelegationRewards(ctx, user2, val2, AllianceDenomTwo)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(5_000_000), rewards2.AmountOf(bondDenom))
}

func TestRewardClaimingAfterRatesDecay(t *testing.T) {
	var err error
	app, ctx := createTestContext(t)
	bondDenom, err := app.StakingKeeper.BondDenom(ctx)
	require.NoError(t, err)
	startTime := time.Now().UTC()
	ctx = ctx.WithBlockTime(startTime).WithBlockHeight(1)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{},
	})
	rewardStartDelay, err := app.AllianceKeeper.RewardDelayTime(ctx)
	require.NoError(t, err)

	// Set tax and rewards to be zero for easier calculation
	distParams, err := app.DistrKeeper.Params.Get(ctx)
	require.NoError(t, err)
	distParams.CommunityTax = sdkmath.LegacyZeroDec()

	err = app.DistrKeeper.Params.Set(ctx, distParams)
	require.NoError(t, err)

	// Accounts
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 5, sdk.NewCoins(
		sdk.NewCoin(bondDenom, sdkmath.NewInt(1_000_000_000_000)),
		sdk.NewCoin(AllianceDenom, sdkmath.NewInt(5_000_000)),
		sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(5_000_000)),
	))

	// Increase the stake on genesis validator
	delegations, err := app.StakingKeeper.GetAllDelegations(ctx)
	require.NoError(t, err)
	require.Len(t, delegations, 1)
	valAddr0, err := sdk.ValAddressFromBech32(delegations[0].ValidatorAddress)
	require.NoError(t, err)
	_val0, _ := app.StakingKeeper.GetValidator(ctx, valAddr0)
	_, err = app.StakingKeeper.Delegate(ctx, addrs[4], sdkmath.NewInt(9_000_000), stakingtypes.Unbonded, _val0, true)
	require.NoError(t, err)

	val0, _ := app.AllianceKeeper.GetAllianceValidator(ctx, []byte(_val0.GetOperator()))
	require.NoError(t, err)

	// Pass a proposal to add a new asset with a huge decay rate
	decayInterval := time.Minute
	decayRate := sdkmath.LegacyMustNewDecFromStr("0.5")
	err = app.AllianceKeeper.CreateAlliance(ctx, &types.MsgCreateAllianceProposal{
		Title:                "",
		Description:          "",
		Denom:                AllianceDenom,
		RewardWeight:         sdkmath.LegacyNewDec(1),
		RewardWeightRange:    types.RewardWeightRange{Min: sdkmath.LegacyNewDec(0), Max: sdkmath.LegacyNewDec(5)},
		TakeRate:             sdkmath.LegacyZeroDec(),
		RewardChangeRate:     decayRate,
		RewardChangeInterval: decayInterval,
	})
	require.NoError(t, err)

	// Pass a proposal to add another new asset no decay
	err = app.AllianceKeeper.CreateAlliance(ctx, &types.MsgCreateAllianceProposal{
		Title:                "",
		Description:          "",
		Denom:                AllianceDenomTwo,
		RewardWeight:         sdkmath.LegacyNewDec(1),
		RewardWeightRange:    types.RewardWeightRange{Min: sdkmath.LegacyNewDec(0), Max: sdkmath.LegacyNewDec(5)},
		TakeRate:             sdkmath.LegacyZeroDec(),
		RewardChangeRate:     sdkmath.LegacyOneDec(),
		RewardChangeInterval: time.Duration(0),
	})
	require.NoError(t, err)

	// Delegate to validator
	_, err = app.AllianceKeeper.Delegate(ctx, addrs[1], val0, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(5_000_000)))
	require.NoError(t, err)

	_, err = app.AllianceKeeper.Delegate(ctx, addrs[1], val0, sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(5_000_000)))
	require.NoError(t, err)
	//
	assets := app.AllianceKeeper.GetAllAssets(ctx)
	err = app.AllianceKeeper.RebalanceHook(ctx, assets)
	require.NoError(t, err)

	// Move block time to trigger 2 decays
	ctx = ctx.WithBlockTime(ctx.BlockTime().Add(decayInterval * 2).Add(rewardStartDelay)).WithBlockHeight(ctx.BlockHeight() + 1)
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, addrs[0], val0, sdk.NewCoins(sdk.NewCoin(bondDenom, sdkmath.NewInt(1000_000))))
	require.NoError(t, err)
	assets = app.AllianceKeeper.GetAllAssets(ctx)

	// Running the decay hook should update reward weight
	err = app.AllianceKeeper.RewardWeightChangeHook(ctx, assets)
	require.NoError(t, err)
	asset, _ := app.AllianceKeeper.GetAssetByDenom(ctx, AllianceDenom)
	require.Equal(t, sdkmath.LegacyMustNewDecFromStr("0.25"), asset.RewardWeight)
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, addrs[0], val0, sdk.NewCoins(sdk.NewCoin(bondDenom, sdkmath.NewInt(1000_000))))
	require.NoError(t, err)

	coins, err := app.AllianceKeeper.ClaimDelegationRewards(ctx, addrs[1], val0, AllianceDenom)
	require.NoError(t, err)
	coins2, err := app.AllianceKeeper.ClaimDelegationRewards(ctx, addrs[1], val0, AllianceDenomTwo)
	require.NoError(t, err)

	// Expect total claimed rewards to be whatever that was added
	require.Equal(t, sdkmath.NewInt(2000_000), coins.Add(coins2...).AmountOf(bondDenom))
}

func TestClaimRewardsAfterRebalancing(t *testing.T) {
	var err error
	app, ctx := createTestContext(t)
	mintPoolAddr := app.AccountKeeper.GetModuleAddress(minttypes.ModuleName)
	startTime := time.Now()
	ctx = ctx.WithBlockTime(startTime)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			types.NewAllianceAsset(AllianceDenom, sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(0), sdkmath.LegacyNewDec(5), sdkmath.LegacyNewDec(0), ctx.BlockTime()),
			types.NewAllianceAsset(AllianceDenomTwo, sdkmath.LegacyNewDec(10), sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(12), sdkmath.LegacyNewDec(0), ctx.BlockTime()),
		},
	})

	// Set tax and rewards to be zero for easier calculation
	distParams, err := app.DistrKeeper.Params.Get(ctx)
	require.NoError(t, err)
	distParams.CommunityTax = sdkmath.LegacyZeroDec()

	err = app.DistrKeeper.Params.Set(ctx, distParams)
	require.NoError(t, err)

	// Accounts
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 4, sdk.NewCoins(
		sdk.NewCoin(AllianceDenom, sdkmath.NewInt(20_000_000)),
		sdk.NewCoin(AllianceDenomTwo, sdkmath.NewInt(2000_000)),
	))
	pks := test_helpers.CreateTestPubKeys(2)

	// Creating two validators: 1 with 0% commission, 1 with 100% commission
	valAddr1 := sdk.ValAddress(addrs[0])
	_val1 := teststaking.NewValidator(t, valAddr1, pks[0])
	_val1.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdkmath.LegacyNewDec(0),
			MaxRate:       sdkmath.LegacyNewDec(0),
			MaxChangeRate: sdkmath.LegacyNewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val1)

	valAddr2 := sdk.ValAddress(addrs[1])
	_val2 := teststaking.NewValidator(t, valAddr2, pks[1])
	_val2.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdkmath.LegacyNewDec(1),
			MaxRate:       sdkmath.LegacyNewDec(1),
			MaxChangeRate: sdkmath.LegacyNewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val2)

	user1 := addrs[2]
	val1, _ := app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	val2, _ := app.AllianceKeeper.GetAllianceValidator(ctx, valAddr2)

	// Mint tokens
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake2", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)

	// New delegation from user 1
	_, err = app.AllianceKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)
	// New delegation from user 2
	_, err = app.AllianceKeeper.Delegate(ctx, user1, val2, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)

	assets := app.AllianceKeeper.GetAllAssets(ctx)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(2000_000))))
	require.NoError(t, err)

	// Transfer another token to fee collector pool
	err = app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, authtypes.FeeCollectorName, sdk.NewCoins(sdk.NewCoin("stake2", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)

	// User 1 delegates more tokens
	_, err = app.AllianceKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)

	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	// Distribute in the next begin block
	// At the next begin block, tokens will be distributed from the fee pool
	cons1, _ := val1.GetConsAddr()
	cons2, _ := val2.GetConsAddr()
	var votingPower int64 = 3
	app.DistrKeeper.AllocateTokens(ctx, votingPower, []abcitypes.VoteInfo{
		{
			Validator: abcitypes.Validator{
				Address: cons1,
				Power:   2,
			},
			BlockIdFlag: cmtproto.BlockIDFlagCommit,
		},
		{
			Validator: abcitypes.Validator{
				Address: cons2,
				Power:   1,
			},
			BlockIdFlag: cmtproto.BlockIDFlagCommit,
		},
	})

	assets = app.AllianceKeeper.GetAllAssets(ctx)
	err = app.AllianceKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	val1, _ = app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)
	rewards, err := app.AllianceKeeper.ClaimDelegationRewards(ctx, user1, val1, AllianceDenom)
	require.NoError(t, err)
	require.Len(t, rewards, 1)
}

func TestRewardWeightWithZeroTokens(t *testing.T) {
	var err error
	app, ctx := createTestContext(t)
	mintPoolAddr := app.AccountKeeper.GetModuleAddress(minttypes.ModuleName)
	startTime := time.Now()
	ctx = ctx.WithBlockTime(startTime)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			types.NewAllianceAsset(AllianceDenom, sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(0), sdkmath.LegacyNewDec(5), sdkmath.LegacyNewDec(0), ctx.BlockTime()),
			types.NewAllianceAsset(AllianceDenomTwo, sdkmath.LegacyNewDec(10), sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(12), sdkmath.LegacyNewDec(0), ctx.BlockTime()),
		},
	})

	// Set tax and rewards to be zero for easier calculation
	distParams, err := app.DistrKeeper.Params.Get(ctx)
	require.NoError(t, err)
	distParams.CommunityTax = sdkmath.LegacyZeroDec()

	err = app.DistrKeeper.Params.Set(ctx, distParams)
	require.NoError(t, err)

	// Accounts
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 4, sdk.NewCoins(
		sdk.NewCoin(AllianceDenom, sdkmath.NewInt(20_000_000)),
	))
	pks := test_helpers.CreateTestPubKeys(2)

	// Creating two validators: 1 with 0% commission, 1 with 100% commission
	valAddr1 := sdk.ValAddress(addrs[0])
	_val1 := teststaking.NewValidator(t, valAddr1, pks[0])
	_val1.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdkmath.LegacyNewDec(0),
			MaxRate:       sdkmath.LegacyNewDec(0),
			MaxChangeRate: sdkmath.LegacyNewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val1)
	user1 := addrs[2]
	val1, _ := app.AllianceKeeper.GetAllianceValidator(ctx, valAddr1)

	// Mint tokens
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(4000_000))))
	require.NoError(t, err)

	// New delegation from user 1
	_, err = app.AllianceKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)

	// Apply take weight to reduce tokens in asset
	asset, found := app.AllianceKeeper.GetAssetByDenom(ctx, AllianceDenom)
	require.True(t, found)
	asset.TotalTokens = sdkmath.NewInt(1)
	app.AllianceKeeper.SetAsset(ctx, asset)

	// New delegation from user 1
	_, err = app.AllianceKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(AllianceDenom, sdkmath.NewInt(1000_000)))
	require.NoError(t, err)

	// Apply take weight to reduce tokens in asset
	asset, found = app.AllianceKeeper.GetAssetByDenom(ctx, AllianceDenom)
	require.True(t, found)
	asset.TotalTokens = sdkmath.NewInt(0)
	app.AllianceKeeper.SetAsset(ctx, asset)

	// Before transfer to reward pool
	beforeMintPoolAmount := app.BankKeeper.GetBalance(ctx, mintPoolAddr, AllianceDenom)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.AllianceKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdkmath.NewInt(2000_000))))
	require.NoError(t, err)

	afterMintPoolAmount := app.BankKeeper.GetBalance(ctx, mintPoolAddr, AllianceDenom)
	require.NoError(t, err)

	require.Equal(t, beforeMintPoolAmount, afterMintPoolAmount)
}
