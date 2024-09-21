package main

import (
	"context"
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
	total, _, err := expire.Count(context.Background(), options.MongoOpts)
	tbr_errors.ExitOnExpectedError(err, "Error in MongoDB interaction", 10)
	expired, err := expire.Expire(context.Background(), options.MongoOpts)
	tbr_errors.ExitOnExpectedError(err, "Error in MongoDB interaction", 10)
	slog.Info("Tokens expired", "total", total, "expired", expired)
}
