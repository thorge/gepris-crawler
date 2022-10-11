package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"time"

	"go4.org/must"
	"golang.org/x/net/publicsuffix"
)

var client http.Client
var ticker = make(chan time.Time, maxRequestsPerSec)

// fetchPage downloads the given URL end returns the HTML.  If the server
// answers with a 5xx status, the request is retried up to triesLeft times.
func fetchPage(url string) (string, error) {
	logger.Printf("INFO: fetching %v", url)
	var body string
	triesLeft := triesLeft
	for {
		<-ticker
		resp, err := client.Get(url)
		if err == nil {
			bodyRaw, err := io.ReadAll(resp.Body)
			check(err)
			must.Close(resp.Body)
			body = string(bodyRaw)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				break
			} else {
				logger.Printf("WARNING: The HTTP request for %v failed with status code %v: %v",
					url, resp.StatusCode, body)
				if resp.StatusCode < 500 || resp.StatusCode >= 600 {
					return "", fmt.Errorf("Giving up on %v.", url)
				}
			}
		} else {
			logger.Printf("WARNING: The HTTP request for %v failed without status code: %v", url, err)
		}
		triesLeft--
		if triesLeft == 0 {
			return "", fmt.Errorf("Giving up on %v.", url)
		}
		time.Sleep(time.Duration(rand.Int31n(maxWaitMillisec)) * time.Millisecond)
	}
	return body, nil
}

func enableTicker(ctx context.Context) {
	for t := range time.Tick(time.Second / maxRequestsPerSec) {
		select {
		case <-ctx.Done():
			return
		case ticker <- t:
		default:
			logger.Println("DEBUG: Workers slower than rate limiter")
		}
	}
}

func init() {
	var transport = &http.Transport{
		// Dial is the original function, plus a line in the log.  This way,
		// one can see how many HTTP connections are established.
		Dial: func(network, addr string) (net.Conn, error) {
			logger.Println("DEBUG: Dial!")
			return net.Dial(network, addr)
		},
		MaxIdleConnsPerHost: numberProjectWorkers + 1,
	}
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	check(err)
	client = http.Client{Transport: transport, Jar: jar}
}
