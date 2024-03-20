package tests_test

import (
	"testing"
	"time"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"

	"github.com/terra-money/alliance/x/alliance/keeper"
	"github.com/terra-money/alliance/x/alliance/types"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/stretchr/testify/require"
)

func TestCreateAlliance(t *testing.T) {
	// GIVEN
	app, ctx := createTestContext(t)
	startTime := time.Now()
	ctx.WithBlockTime(startTime).WithBlockHeight(1)
	queryServer := keeper.NewQueryServerImpl(app.AllianceKeeper)
	rewardDuration, err := app.AllianceKeeper.RewardDelayTime(ctx)
	require.NoError(t, err)

	// WHEN
	createErr := app.AllianceKeeper.CreateAlliance(ctx, &types.MsgCreateAllianceProposal{
		Title:             "",
		Description:       "",
		Denom:             "uluna",
		RewardWeight:      sdkmath.LegacyOneDec(),
		RewardWeightRange: types.RewardWeightRange{Min: sdkmath.LegacyNewDec(0), Max: sdkmath.LegacyNewDec(5)},
		TakeRate:          sdkmath.LegacyOneDec(),
	})
	alliancesRes, alliancesErr := queryServer.Alliances(ctx, &types.QueryAlliancesRequest{})

	// THEN
	require.Nil(t, createErr)
	require.Nil(t, alliancesErr)
	require.Equal(t, alliancesRes, &types.QueryAlliancesResponse{
		Alliances: []types.AllianceAsset{
			{
				Denom:                "uluna",
				RewardWeight:         sdkmath.LegacyNewDec(1),
				RewardWeightRange:    types.RewardWeightRange{Min: sdkmath.LegacyNewDec(0), Max: sdkmath.LegacyNewDec(5)},
				TakeRate:             sdkmath.LegacyNewDec(1),
				TotalTokens:          sdkmath.ZeroInt(),
				TotalValidatorShares: sdkmath.LegacyNewDec(0),
				RewardStartTime:      ctx.BlockTime().Add(rewardDuration),
				RewardChangeRate:     sdkmath.LegacyNewDec(0),
				RewardChangeInterval: 0,
				LastRewardChangeTime: ctx.BlockTime().Add(rewardDuration),
			},
		},
		Pagination: &query.PageResponse{
			NextKey: nil,
			Total:   1,
		},
	})
}

func TestCreateAllianceFailWithDuplicatedDenom(t *testing.T) {
	// GIVEN
	app, ctx := createTestContext(t)
	startTime := time.Now()
	ctx.WithBlockTime(startTime).WithBlockHeight(1)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			types.NewAllianceAsset("uluna", sdkmath.LegacyNewDec(1), sdkmath.LegacyZeroDec(), sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(0), startTime),
		},
	})

	// WHEN
	createErr := app.AllianceKeeper.CreateAlliance(ctx, &types.MsgCreateAllianceProposal{
		Title:        "",
		Description:  "",
		Denom:        "uluna",
		RewardWeight: sdkmath.LegacyOneDec(),
		TakeRate:     sdkmath.LegacyOneDec(),
	})

	// THEN
	require.Error(t, createErr)
}

func TestUpdateAlliance(t *testing.T) {
	// GIVEN
	app, ctx := createTestContext(t)
	startTime := time.Now()
	ctx.WithBlockTime(startTime).WithBlockHeight(1)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			{
				Denom:                "uluna",
				RewardWeight:         sdkmath.LegacyNewDec(2),
				RewardWeightRange:    types.RewardWeightRange{Min: sdkmath.LegacyNewDec(0), Max: sdkmath.LegacyNewDec(10)},
				TakeRate:             sdkmath.LegacyOneDec(),
				TotalTokens:          sdkmath.ZeroInt(),
				TotalValidatorShares: sdkmath.LegacyNewDec(0),
			},
		},
	})
	queryServer := keeper.NewQueryServerImpl(app.AllianceKeeper)

	// WHEN
	updateErr := app.AllianceKeeper.UpdateAlliance(ctx, &types.MsgUpdateAllianceProposal{
		Title:                "",
		Description:          "",
		Denom:                "uluna",
		RewardWeight:         sdkmath.LegacyNewDec(6),
		TakeRate:             sdkmath.LegacyNewDec(7),
		RewardChangeInterval: 0,
		RewardChangeRate:     sdkmath.LegacyZeroDec(),
	})
	alliancesRes, alliancesErr := queryServer.Alliances(ctx, &types.QueryAlliancesRequest{})

	// THEN
	require.Nil(t, updateErr)
	require.Nil(t, alliancesErr)
	require.Equal(t, alliancesRes, &types.QueryAlliancesResponse{
		Alliances: []types.AllianceAsset{
			{
				Denom:                "uluna",
				RewardWeight:         sdkmath.LegacyNewDec(6),
				RewardWeightRange:    types.RewardWeightRange{Min: sdkmath.LegacyNewDec(0), Max: sdkmath.LegacyNewDec(10)},
				TakeRate:             sdkmath.LegacyNewDec(7),
				TotalTokens:          sdkmath.ZeroInt(),
				TotalValidatorShares: sdkmath.LegacyNewDec(0),
				RewardChangeRate:     sdkmath.LegacyNewDec(0),
				RewardChangeInterval: 0,
			},
		},
		Pagination: &query.PageResponse{
			NextKey: nil,
			Total:   1,
		},
	})
}

func TestDeleteAlliance(t *testing.T) {
	// GIVEN
	app, ctx := createTestContext(t)
	startTime := time.Now()
	ctx.WithBlockTime(startTime).WithBlockHeight(1)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			{
				Denom:        "uluna",
				RewardWeight: sdkmath.LegacyNewDec(2),
				TakeRate:     sdkmath.LegacyOneDec(),
				TotalTokens:  sdkmath.ZeroInt(),
			},
		},
	})
	queryServer := keeper.NewQueryServerImpl(app.AllianceKeeper)

	// WHEN
	deleteErr := app.AllianceKeeper.DeleteAlliance(ctx, &types.MsgDeleteAllianceProposal{
		Denom: "uluna",
	})
	alliancesRes, alliancesErr := queryServer.Alliances(ctx, &types.QueryAlliancesRequest{})

	// THEN
	require.Nil(t, deleteErr)
	require.Nil(t, alliancesErr)
	require.Equal(t, alliancesRes, &types.QueryAlliancesResponse{
		Alliances: nil,
		Pagination: &query.PageResponse{
			NextKey: nil,
			Total:   0,
		},
	})
}

func TestUpdateParams(t *testing.T) {
	// GIVEN
	app, ctx := createTestContext(t)
	startTime := time.Now()
	ctx.WithBlockTime(startTime).WithBlockHeight(1)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			types.NewAllianceAsset("uluna", sdkmath.LegacyNewDec(1), sdkmath.LegacyZeroDec(), sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(0), startTime),
		},
	})
	timeNow := time.Now().UTC()
	govAddr := authtypes.NewModuleAddress(govtypes.ModuleName).String()

	// WHEN
	msgServer := keeper.MsgServer{Keeper: app.AllianceKeeper}
	_, err := msgServer.UpdateParams(sdk.WrapSDKContext(ctx), &types.MsgUpdateParams{
		Authority: govAddr,
		Params: types.Params{
			RewardDelayTime:       100,
			TakeRateClaimInterval: 100,
			LastTakeRateClaimTime: timeNow,
		},
	})
	require.NoError(t, err)

	// THEN
	params, err := app.AllianceKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, time.Duration(100), params.RewardDelayTime)
	require.Equal(t, time.Duration(100), params.TakeRateClaimInterval)
	require.Equal(t, timeNow, params.LastTakeRateClaimTime)
}

func TestUnauthorizedUpdateParams(t *testing.T) {
	// GIVEN
	app, ctx := createTestContext(t)
	startTime := time.Now()
	ctx.WithBlockTime(startTime).WithBlockHeight(1)
	app.AllianceKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.AllianceAsset{
			types.NewAllianceAsset("uluna", sdkmath.LegacyNewDec(1), sdkmath.LegacyZeroDec(), sdkmath.LegacyNewDec(2), sdkmath.LegacyNewDec(0), startTime),
		},
	})
	timeNow := time.Now().UTC()

	// WHEN
	msgServer := keeper.MsgServer{Keeper: app.AllianceKeeper}
	_, err := msgServer.UpdateParams(sdk.WrapSDKContext(ctx), &types.MsgUpdateParams{
		Authority: sdk.MustBech32ifyAddressBytes(sdk.GetConfig().GetBech32AccountAddrPrefix(), []byte("random")),
		Params: types.Params{
			RewardDelayTime:       100,
			TakeRateClaimInterval: 100,
			LastTakeRateClaimTime: timeNow,
		},
	})

	// THEN
	require.NotNil(t, err)
}
