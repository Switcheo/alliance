package keeper

import (
	"time"

	"github.com/terra-money/alliance/x/alliance/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

func (k Keeper) RewardDelayTime(ctx sdk.Context) (res time.Duration, err error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return res, err
	}
	return params.RewardDelayTime, nil
}

func (k Keeper) RewardClaimInterval(ctx sdk.Context) (res time.Duration, err error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return res, err
	}
	return params.TakeRateClaimInterval, nil
}

func (k Keeper) LastRewardClaimTime(ctx sdk.Context) (res time.Time, err error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return res, err
	}
	return params.LastTakeRateClaimTime, nil
}

func (k Keeper) SetLastRewardClaimTime(ctx sdk.Context, lastTime time.Time) error {
	params, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	params.LastTakeRateClaimTime = lastTime
	return k.SetParams(ctx, params)
}

func (k Keeper) GetParams(ctx sdk.Context) (params types.Params, err error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.ParamsKey)
	if err != nil {
		return params, err
	}
	if bz == nil {
		return
	}
	k.cdc.MustUnmarshal(bz, &params)
	return
}

func (k Keeper) SetParams(ctx sdk.Context, params types.Params) error {
	if err := types.ValidatePositiveDuration(params.RewardDelayTime); err != nil {
		return err
	}
	if err := types.ValidatePositiveDuration(params.TakeRateClaimInterval); err != nil {
		return err
	}
	store := k.storeService.OpenKVStore(ctx)
	bz := k.cdc.MustMarshal(&params)
	store.Set(types.ParamsKey, bz)
	return nil
}
