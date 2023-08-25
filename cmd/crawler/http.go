package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"time"

	tbr_logging "gitlab.com/bronger/tools/logging"
	"go4.org/must"
	"golang.org/x/net/publicsuffix"
)

var client http.Client

// fetchPage downloads the given URL end returns the HTML.  If the server
// answers with a 5xx status, the request is retried up to triesLeft times.
func fetchPage(logger tbr_logging.Logger, url string) (string, error) {
	logger.Info("Fetching URL", "url", url)
	var body string
	triesLeft := triesLeft
	for {
		<-ticker
		resp, err := client.Get(url)
		if err == nil {
			bodyRaw, err := io.ReadAll(resp.Body)
			if err != nil {
				return "", err
			}
			must.Close(resp.Body)
			body = string(bodyRaw)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				break
			} else {
				logger.Warn("The HTTP request failed",
					"url", url, "status code", resp.StatusCode, "message", body)
				if resp.StatusCode < 500 || resp.StatusCode >= 600 {
					return "", fmt.Errorf("Giving up on %v.", url)
				}
			}
		} else {
			logger.Warn("The HTTP request failed without status code", "url", url, "error", err)
		}
		triesLeft--
		if triesLeft == 0 {
			return "", fmt.Errorf("Giving up on %v.", url)
		}
		time.Sleep(time.Duration(rand.Int31n(maxWaitMillisec)) * time.Millisecond)
	}
	return body, nil
}

var ticker = make(chan time.Time, maxRequestsPerSec)

// enableTitle must be run as a Go routine that may be cancelled by the given
// context.  It fills the global variable “ticker” with events at a frequency
// of maxRequestsPerSec.
func enableTicker(ctx context.Context, logger tbr_logging.Logger) {
	for t := range time.Tick(time.Second / maxRequestsPerSec) {
		select {
		case <-ctx.Done():
			return
		case ticker <- t:
		default:
			logger.Debug("Workers slower than rate limiter")
		}
	}
}

// This init function initialises the global “client” for fetchPage.
func init() {
	var transport = &http.Transport{
		// Dial is the original function, plus a line in the log.  This way,
		// one can see how many HTTP connections are established.
		Dial: func(network, addr string) (net.Conn, error) {
			slog.Debug("Dial!")
			return net.Dial(network, addr)
		},
		// “+ 1” for the connection for search results pages
		MaxIdleConnsPerHost: numberProjectWorkers + 1,
	}
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		panic(err)
	}
	client = http.Client{Transport: transport, Jar: jar}
}
