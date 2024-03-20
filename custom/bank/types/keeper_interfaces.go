package types

import "context"

type StakingKeeper interface {
	BondDenom(ctx context.Context) (res string, err error)
}
