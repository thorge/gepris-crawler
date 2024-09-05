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
	pmh.Serve()
}
