package main

import (
	"bytes"
	"context"
	"encoding/json"
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

// requestJSON performs an HTTP request against the GEPRIS backend API and
// returns the response body.  If “payload” is not nil, it is encoded as JSON
// and sent as the request body using the given method (usually POST);
// otherwise a GET request is made.  The request is retried up to triesLeft
// times if the server answers with a 5xx status, with a network error, or with
// a body that is not valid JSON (which indicates that the request was
// intercepted by the Web application firewall).  A JSON response with a 4xx
// status is a permanent error and is not retried.
func requestJSON(logger tbr_logging.Logger, method, url string, payload any) (raw []byte, err error) {
	logger.Info("Fetching URL", "url", url)
	var payloadRaw []byte
	if payload != nil {
		if payloadRaw, err = json.Marshal(payload); err != nil {
			return nil, err
		}
	}
	triesLeft := triesLeft
	for {
		<-ticker
		var resp *http.Response
		if payloadRaw == nil {
			resp, err = client.Get(url)
		} else {
			req, reqErr := http.NewRequest(method, url, bytes.NewReader(payloadRaw))
			if reqErr != nil {
				return nil, reqErr
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err = client.Do(req)
		}
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			must.Close(resp.Body)
			if readErr != nil {
				err = readErr
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 && json.Valid(body) {
				return body, nil
			} else if resp.StatusCode >= 400 && resp.StatusCode < 500 && json.Valid(body) {
				logger.Warn("The HTTP request was rejected",
					"url", url, "status code", resp.StatusCode, "message", string(body))
				return nil, fmt.Errorf("Giving up on %v.", url)
			} else {
				logger.Warn("The HTTP request failed",
					"url", url, "status code", resp.StatusCode,
					"valid json", json.Valid(body))
			}
		} else {
			logger.Warn("The HTTP request failed without status code", "url", url, "error", err)
		}
		triesLeft--
		if triesLeft == 0 {
			return nil, fmt.Errorf("Giving up on %v.", url)
		}
		time.Sleep(time.Duration(rand.Int31n(maxWaitMillisec)) * time.Millisecond)
	}
}

// fetchJSON downloads the given URL and returns the JSON response body.  See
// requestJSON for the retrying behaviour.
func fetchJSON(logger tbr_logging.Logger, url string) ([]byte, error) {
	return requestJSON(logger, http.MethodGet, url, nil)
}

// postJSON sends the given payload encoded as JSON to the given URL and
// returns the JSON response body.  See requestJSON for the retrying behaviour.
func postJSON(logger tbr_logging.Logger, url string, payload any) ([]byte, error) {
	return requestJSON(logger, http.MethodPost, url, payload)
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

// This init function initialises the global “client” for requestJSON.
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
