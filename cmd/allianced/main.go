package main

import (
	"os"

	"github.com/terra-money/alliance/app"
	"github.com/terra-money/alliance/cmd/allianced/cmd"

	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"
)

func main() {
	//	app.SetAddressPrefixes() //TODO: either implement or remove
	rootCmd, _ := cmd.NewRootCmd()

	if err := svrcmd.Execute(rootCmd, "ALLIANCED", app.DefaultNodeHome); err != nil {
		os.Exit(1)
	}
}
