package main

import (
	"log/slog"
	"os"

	tbr_errors "gitlab.com/bronger/tools/errors"
	tbr_logging "gitlab.com/bronger/tools/logging"
	"jugit.fz-juelich.de/fdm/gepris-crawler/internal/options"
	"jugit.fz-juelich.de/fdm/pmh-server/expire"
)

func init() {
	tbr_logging.Init(os.Stderr, slog.LevelInfo)
}

func main() {
	count, err := expire.Expire(options.MongoOpts)
	tbr_errors.ExitOnExpectedError(err, "Error in MongoDB interaction", 10)
	slog.Info("Tokens expired", "number", count)
}
