package cmd

import (
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/GenesisKernel/go-genesis/packages/conf"
	"github.com/GenesisKernel/go-genesis/packages/model"
)

// initDatabaseCmd represents the initDatabase command
var initDatabaseCmd = &cobra.Command{
	Use:    "initDatabase",
	Short:  "Initializing database",
	PreRun: loadConfigWKey,
	Run: func(cmd *cobra.Command, args []string) {
		if err := model.InitDB(conf.Config.DB); err != nil {
			log.WithError(err).Fatal("init db")
		}
	},
}
