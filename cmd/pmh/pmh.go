/*
pmh offers a web service so that GEPRIS data can be retrieved using OAI-PMH.
It takes its data from a MogoDB.

See the README file for further information.
*/
package main

import (
	"jugit.fz-juelich.de/fdm/pmh-server"
)

func main() {
	pmh.Serve(pmh.Options{
		DBName:              "gepris",
		CollName:            "projects",
		AddIndices:          []string{"endYear"},
		Name:                "GEPRIS – project database of the Deutsche Forschungsgemeinschaft (DFG); UNOFFICIAL",
		URL:                 "https://gepris.its.kfa-juelich.de",
		AdminEmail:          "juser@fz-juelich.de",
		BuildFunc:           buildGrant,
		BuildDCFunc:         buildGrantDC,
		BuildOnlyHeaderFunc: buildGrantOnlyHeader,
	})
}
