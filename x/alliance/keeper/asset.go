package keeper

import (
	"context"
	"fmt"
	"math"
	"time"

	sdkmath "cosmossdk.io/math"
	"cosmossdk.io/store"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/terra-money/alliance/x/alliance/types"
)

// InitializeAllianceAssets this hooks adds a reward change snapshot when time > asset.RewardStartTime
// A reward change snapshot of 0 weight is added to signify that the asset did not accrue any rewards during the
// warm up period so we can calculate the correct rewards when claiming
func (k Keeper) InitializeAllianceAssets(ctx sdk.Context, assets []*types.AllianceAsset) {
	for _, asset := range assets {
		if asset.IsInitialized || !asset.RewardsStarted(ctx.BlockTime()) {
			continue
		}
		asset.IsInitialized = true
		k.IterateAllianceValidatorInfo(ctx, func(valAddr sdk.ValAddress, info types.AllianceValidatorInfo) bool {
			k.CreateInitialRewardWeightChangeSnapshot(ctx, asset.Denom, valAddr, info)
			return false
		})
		k.SetAsset(ctx, *asset)
	}
}

// UpdateAllianceAsset updates the alliance asset with new params
// Also saves a snapshot whenever rewards weight changes to make sure delegation reward calculation has reference to
// historical reward rates
func (k Keeper) UpdateAllianceAsset(ctx sdk.Context, newAsset types.AllianceAsset) error {
	asset, found := k.GetAssetByDenom(ctx, newAsset.Denom)
	if !found {
		return types.ErrUnknownAsset
	}

	var err error
	if newAsset.RewardWeightRange.Min.GT(newAsset.RewardWeight) || newAsset.RewardWeightRange.Max.LT(newAsset.RewardWeight) {
		err = types.ErrRewardWeightOutOfBound
		return err
	}
	// Only add a snapshot if reward weight changes
	if !newAsset.RewardWeight.Equal(asset.RewardWeight) {
		k.IterateAllianceValidatorInfo(ctx, func(valAddr sdk.ValAddress, info types.AllianceValidatorInfo) bool {
			var validator types.AllianceValidator
			validator, err = k.GetAllianceValidator(ctx, valAddr)
			if err != nil {
				return true
			}
			_, err = k.ClaimValidatorRewards(ctx, validator)
			if err != nil {
				return true
			}
			k.SetRewardWeightChangeSnapshot(ctx, asset, validator)
			return false
		})
		if err != nil {
			return err
		}
		// Queue a re-balancing event if reward weight change
		k.QueueAssetRebalanceEvent(ctx)
	}

	// If there was a change in reward decay rate or reward decay time
	if !newAsset.RewardChangeRate.Equal(asset.RewardChangeRate) || newAsset.RewardChangeInterval != asset.RewardChangeInterval {
		// And if there were no reward changes scheduled previously, start the counter from now
		if asset.RewardChangeRate.Equal(sdkmath.LegacyOneDec()) || asset.RewardChangeInterval == 0 {
			newAsset.LastRewardChangeTime = ctx.BlockTime()
		}
		// Else do nothing since there is already a change that was scheduled.
		// The next trigger will use the new reward change and reward interval
		// following triggers will be scheduled using the new reward change interval
	}

	// Make sure only whitelisted fields can be updated
	asset.TakeRate = newAsset.TakeRate
	asset.RewardWeight = newAsset.RewardWeight
	asset.RewardChangeRate = newAsset.RewardChangeRate
	asset.RewardChangeInterval = newAsset.RewardChangeInterval
	asset.LastRewardChangeTime = newAsset.LastRewardChangeTime
	k.SetAsset(ctx, asset)

	return nil
}

func (k Keeper) RebalanceHook(ctx sdk.Context, assets []*types.AllianceAsset) error {
	if k.ConsumeAssetRebalanceEvent(ctx) {
		return k.RebalanceBondTokenWeights(ctx, assets)
	}
	return nil
}

// RebalanceBondTokenWeights uses asset reward weights to calculate the expected amount of staking token that has to be
// minted / burned to maintain the right ratio
// It iterates all validators and calculates the expected staked amount based on delegations and delegates/undelegates
// the difference.
func (k Keeper) RebalanceBondTokenWeights(ctx sdk.Context, assets []*types.AllianceAsset) (err error) {
	moduleAddr := k.accountKeeper.GetModuleAddress(types.ModuleName)
	allianceBondAmount := k.GetAllianceBondedAmount(ctx, moduleAddr)

	totalBondedTokens, err := k.stakingKeeper.TotalBondedTokens(ctx)
	if err != nil {
		return err
	}

	nativeBondAmount := totalBondedTokens.Sub(allianceBondAmount)
	bondDenom, err := k.stakingKeeper.BondDenom(ctx)
	if err != nil {
		return err
	}

	unbondedValidatorShares := sdk.NewDecCoins()
	var bondedValidators []types.AllianceValidator

	// Iterate through all alliance validators to remove those that are unbonded.
	// Unbonded validators will be ignored when rebalancing.
	k.IterateAllianceValidatorInfo(ctx, func(valAddr sdk.ValAddress, info types.AllianceValidatorInfo) bool {
		var validator types.AllianceValidator
		validator, err = k.GetAllianceValidator(ctx, valAddr)
		if err != nil {
			return true
		}
		if validator.IsBonded() {
			bondedValidators = append(bondedValidators, validator)
		} else {
			unbondedValidatorShares = unbondedValidatorShares.Add(validator.ValidatorShares...)
		}
		return false
	})
	if err != nil {
		return err
	}

	for _, validator := range bondedValidators {
		currentBondedAmount := sdkmath.LegacyNewDec(0)
		valBz, err := k.GetValidatorAddrBz(validator.GetOperator())
		if err != nil {
			return err
		}
		delegation, err := k.stakingKeeper.GetDelegation(ctx, moduleAddr, valBz)
		if err == nil {
			currentBondedAmount = validator.TokensFromShares(delegation.GetShares())
		}

		expectedBondAmount := sdkmath.LegacyZeroDec()
		for _, asset := range assets {
			// Ignores assets that were recently added to prevent a small set of stakers from owning too much of the
			// voting power at the start. Uses the asset.RewardStartTime to determine when an asset is activated
			if !asset.RewardsStarted(ctx.BlockTime()) {
				// Queue a rebalancing event so that we keep checking if the asset rewards has started in the next block
				k.QueueAssetRebalanceEvent(ctx)
				continue
			}
			valShares := validator.ValidatorSharesWithDenom(asset.Denom)
			expectedBondAmountForAsset := asset.RewardWeight.MulInt(nativeBondAmount)

			bondedValidatorShares := asset.TotalValidatorShares.Sub(unbondedValidatorShares.AmountOf(asset.Denom))
			if valShares.IsPositive() && bondedValidatorShares.IsPositive() {
				expectedBondAmount = expectedBondAmount.Add(valShares.Quo(bondedValidatorShares).Mul(expectedBondAmountForAsset))
			}
		}
		if expectedBondAmount.GT(currentBondedAmount) {
			// delegate more tokens to increase the weight
			bondAmount := expectedBondAmount.Sub(currentBondedAmount).TruncateInt()
			// If bond amount is zero after truncation, then skip delegation
			// Small delegations to alliance will not change the voting power by a lot. We can accumulate all the small
			// changes until it is larger than 1 utoken before we update voting power
			if bondAmount.IsZero() {
				continue
			}
			err = k.bankKeeper.MintCoins(ctx, types.ModuleName, sdk.NewCoins(sdk.NewCoin(bondDenom, bondAmount)))
			if err != nil {
				return err
			}
			_, err = k.ClaimValidatorRewards(ctx, validator)
			if err != nil {
				return err
			}
			_, err = k.stakingKeeper.Delegate(ctx, moduleAddr, bondAmount, stakingtypes.Unbonded, *validator.Validator, true)
			if err != nil {
				return err
			}
		} else if expectedBondAmount.LT(currentBondedAmount) {
			// undelegate more tokens to reduce the weight
			unbondAmount := currentBondedAmount.Sub(expectedBondAmount).TruncateInt()
			// When unbondAmount is < 1 utoken, we ignore the change in voting power since it rounds down to zero.
			if unbondAmount.IsZero() {
				continue
			}
			sharesToUnbond, err := k.stakingKeeper.ValidateUnbondAmount(ctx, moduleAddr, valBz, unbondAmount)
			if err != nil {
				return err
			}
			_, err = k.ClaimValidatorRewards(ctx, validator)
			if err != nil {
				return err
			}
			tokensToBurn, err := k.stakingKeeper.Unbond(ctx, moduleAddr, valBz, sharesToUnbond)
			if err != nil {
				return err
			}
			err = k.bankKeeper.BurnCoins(ctx, stakingtypes.BondedPoolName, sdk.NewCoins(sdk.NewCoin(bondDenom, tokensToBurn)))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// SetAsset Does not check if the asset already exists and overwrites it
func (k Keeper) SetAsset(ctx context.Context, asset types.AllianceAsset) {
	store := k.storeService.OpenKVStore(ctx)
	b := k.cdc.MustMarshal(&asset)
	store.Set(types.GetAssetKey(asset.Denom), b)
}

func (k Keeper) GetAllAssets(ctx sdk.Context) (assets []*types.AllianceAsset) {
	store := k.storeService.OpenKVStore(ctx)
	iter := storetypes.KVStorePrefixIterator(runtime.KVStoreAdapter(store), types.AssetKey)
	defer iter.Close()

	for iter.Valid() {
		var asset types.AllianceAsset
		b := iter.Value()
		k.cdc.MustUnmarshal(b, &asset)
		assets = append(assets, &asset)
		iter.Next()
	}
	return assets
}

func (k Keeper) GetAssetByDenom(ctx context.Context, denom string) (asset types.AllianceAsset, found bool) {
	store := k.storeService.OpenKVStore(ctx)
	assetKey := types.GetAssetKey(denom)
	b, err := store.Get(assetKey)

	if err != nil {
		return asset, false
	}

	if b == nil {
		return asset, false
	}

	k.cdc.MustUnmarshal(b, &asset)
	return asset, true
}

func (k Keeper) DeleteAsset(ctx sdk.Context, asset types.AllianceAsset) error {
	if asset.TotalTokens.GT(sdkmath.ZeroInt()) {
		return fmt.Errorf("cannot delete alliance assets that still have tokens")
	}
	k.deleteAsset(ctx, asset.Denom)
	return nil
}

func (k Keeper) deleteAsset(ctx sdk.Context, denom string) {
	store := k.storeService.OpenKVStore(ctx)
	assetKey := types.GetAssetKey(denom)
	store.Delete(assetKey)
}

func (k Keeper) QueueAssetRebalanceEvent(ctx context.Context) {
	store := k.storeService.OpenKVStore(ctx)
	key := types.AssetRebalanceQueueKey
	store.Set(key, []byte{0x00})
}

func (k Keeper) ConsumeAssetRebalanceEvent(ctx sdk.Context) bool {
	store := k.storeService.OpenKVStore(ctx)
	key := types.AssetRebalanceQueueKey
	b, err := store.Get(key)
	if err != nil {
		return false
	}
	if b == nil {
		return false
	}
	store.Delete(key)
	return true
}

// DeductAssetsHook is called periodically to deduct from an alliance asset (calculated by take_rate).
// The interval in which assets are deducted is set in module params
func (k Keeper) DeductAssetsHook(ctx sdk.Context, assets []*types.AllianceAsset) (sdk.Coins, error) {
	last, err := k.LastRewardClaimTime(ctx)
	if err != nil {
		return nil, err
	}
	interval, err := k.RewardClaimInterval(ctx)
	if err != nil {
		return nil, err
	}

	next := last.Add(interval)
	if ctx.BlockTime().After(next) {
		return k.DeductAssetsWithTakeRate(ctx, last, assets)
	}
	return nil, nil
}

// DeductAssetsWithTakeRate Deducts an alliance asset using the take_rate
// The deducted asset is distributed to the fee_collector module account to be redistributed to stakers
func (k Keeper) DeductAssetsWithTakeRate(ctx sdk.Context, lastClaim time.Time, assets []*types.AllianceAsset) (sdk.Coins, error) {
	var coins sdk.Coins

	// If start time has not been set, set the start time and do nothing for this block
	if lastClaim.Equal(time.Time{}) {
		if err := k.SetLastRewardClaimTime(ctx, ctx.BlockTime()); err != nil {
			return coins, err
		}
		return coins, nil
	}

	rewardClaimInterval, err := k.RewardClaimInterval(ctx)
	if err != nil {
		return nil, err
	}
	durationSinceLastClaim := ctx.BlockTime().Sub(lastClaim)
	intervalsSinceLastClaim := uint64(durationSinceLastClaim / rewardClaimInterval)

	assetsWithPositiveTakeRate := 0

	for _, asset := range assets {
		if asset.TotalTokens.IsPositive() && asset.TakeRate.IsPositive() && asset.RewardsStarted(ctx.BlockTime()) {
			assetsWithPositiveTakeRate++
			// take rate must be < 1 so multiple is also < 1
			multiplier := sdkmath.LegacyOneDec().Sub(asset.TakeRate).Power(intervalsSinceLastClaim)
			oldAmount := asset.TotalTokens
			newAmount := multiplier.MulInt(asset.TotalTokens)
			if newAmount.LTE(sdkmath.LegacyOneDec()) {
				// If the next update reduces the amount of tokens to less than or equal to 1, stop reducing
				continue
			}
			asset.TotalTokens = newAmount.TruncateInt()
			deductedAmount := oldAmount.Sub(asset.TotalTokens)
			coins = coins.Add(sdk.NewCoin(asset.Denom, deductedAmount))
			k.SetAsset(ctx, *asset)
		}
	}

	// If there are no assets with positive take rate, continue to update last reward claim time and return
	if assetsWithPositiveTakeRate == 0 {
		if err := k.SetLastRewardClaimTime(ctx, ctx.BlockTime()); err != nil {
			return coins, err
		}
		return coins, nil
	}

	if !coins.Empty() && !coins.IsZero() {
		err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.ModuleName, k.feeCollectorName, coins)
		if err != nil {
			return nil, err
		}
		// Only update if there was a token transfer to prevent < 1 amounts to be ignored
		if err = k.SetLastRewardClaimTime(ctx, lastClaim.Add(rewardClaimInterval*time.Duration(intervalsSinceLastClaim))); err != nil {
			return coins, err
		}
		_ = ctx.EventManager().EmitTypedEvent(&types.DeductAllianceAssetsEvent{Coins: coins})
	}
	return coins, nil
}

func (k Keeper) SetRewardWeightChangeSnapshot(ctx sdk.Context, asset types.AllianceAsset, val types.AllianceValidator) {
	snapshot := types.NewRewardWeightChangeSnapshot(asset, val)
	valBz, _ := k.GetValidatorAddrBz(val.GetOperator())
	k.setRewardWeightChangeSnapshot(ctx, asset.Denom, valBz, uint64(ctx.BlockHeight()), snapshot)
}

func (k Keeper) CreateInitialRewardWeightChangeSnapshot(ctx sdk.Context, denom string, valAddr sdk.ValAddress, info types.AllianceValidatorInfo) {
	snapshot := types.RewardWeightChangeSnapshot{
		PrevRewardWeight: sdkmath.LegacyZeroDec(),
		RewardHistories:  info.GlobalRewardHistory,
	}
	k.setRewardWeightChangeSnapshot(ctx, denom, valAddr, uint64(ctx.BlockHeight()), snapshot)
}

func (k Keeper) setRewardWeightChangeSnapshot(ctx sdk.Context, denom string, valAddr sdk.ValAddress, height uint64, snapshot types.RewardWeightChangeSnapshot) {
	key := types.GetRewardWeightChangeSnapshotKey(denom, valAddr, height)
	store := k.storeService.OpenKVStore(ctx)
	b := k.cdc.MustMarshal(&snapshot)
	store.Set(key, b)
}

func (k Keeper) IterateWeightChangeSnapshot(ctx context.Context, denom string, valAddr sdk.ValAddress, lastClaimHeight uint64) (store.Iterator, error) {
	store := k.storeService.OpenKVStore(ctx)
	key := types.GetRewardWeightChangeSnapshotKey(denom, valAddr, lastClaimHeight)
	end := types.GetRewardWeightChangeSnapshotKey(denom, valAddr, math.MaxUint64)
	iterator, err := store.Iterator(key, end)
	if err != nil {
		return nil, err
	}
	return iterator, nil
}

func (k Keeper) IterateAllWeightChangeSnapshot(ctx sdk.Context, cb func(denom string, valAddr sdk.ValAddress, lastClaimHeight uint64, snapshot types.RewardWeightChangeSnapshot) (stop bool)) {
	store := k.storeService.OpenKVStore(ctx)
	iter := storetypes.KVStorePrefixIterator(runtime.KVStoreAdapter(store), types.RewardWeightChangeSnapshotKey)
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		var snapshot types.RewardWeightChangeSnapshot
		k.cdc.MustUnmarshal(iter.Value(), &snapshot)
		denom, valAddr, height := types.ParseRewardWeightChangeSnapshotKey(iter.Key())
		if cb(denom, valAddr, height, snapshot) {
			return
		}
	}
}

func (k Keeper) RewardWeightChangeHook(ctx sdk.Context, assets []*types.AllianceAsset) error {
	for _, asset := range assets {
		// If no reward changes are required, skip
		if asset.RewardChangeInterval == 0 || asset.RewardChangeRate.Equal(sdkmath.LegacyOneDec()) {
			continue
		}
		// If it is not scheduled for change, skip
		if asset.LastRewardChangeTime.Add(asset.RewardChangeInterval).After(ctx.BlockTime()) {
			continue
		}
		durationSinceLastClaim := ctx.BlockTime().Sub(asset.LastRewardChangeTime)
		intervalsSinceLastClaim := uint64(durationSinceLastClaim / asset.RewardChangeInterval)

		// Compound the weight changes
		multiplier := asset.RewardChangeRate.Power(intervalsSinceLastClaim)
		asset.RewardWeight = asset.RewardWeight.Mul(multiplier)
		if asset.RewardWeight.LT(asset.RewardWeightRange.Min) {
			asset.RewardWeight = asset.RewardWeightRange.Min
		}
		if asset.RewardWeight.GT(asset.RewardWeightRange.Max) {
			asset.RewardWeight = asset.RewardWeightRange.Max
		}
		asset.LastRewardChangeTime = asset.LastRewardChangeTime.Add(asset.RewardChangeInterval * time.Duration(intervalsSinceLastClaim))
		k.QueueAssetRebalanceEvent(ctx)
		err := k.UpdateAllianceAsset(ctx, *asset)
		if err != nil {
			return err
		}
	}
	return nil
}
