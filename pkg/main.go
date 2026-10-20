package main

import (
	"os"
	// Time zone data for calendar features, compiled in: plugins may not read the file system.
	_ "time/tzdata"

	timestral "github.com/consi/grafana-timestral/pkg/datasource"
	"github.com/grafana/grafana-plugin-sdk-go/backend/datasource"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

func main() {
	if err := datasource.Manage(timestral.PluginID, timestral.NewDatasource, datasource.ManageOpts{}); err != nil {
		log.DefaultLogger.Error(err.Error())
		os.Exit(1)
	}
}
