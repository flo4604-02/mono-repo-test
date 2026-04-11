package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"connectrpc.com/connect"
	pingv1 "github.com/unkeyed/mono-repo-test/gen/ping/v1"
	"github.com/unkeyed/mono-repo-test/gen/ping/v1/pingv1connect"
	"golang.org/x/net/http2"
)

func main() {
	host := "https://local-api-local.unkey.local"
	if h := os.Getenv("HOST"); h != "" {
		host = h
	}

	count := int32(30)
	if c := os.Getenv("COUNT"); c != "" {
		n, _ := strconv.Atoi(c)
		if n > 0 {
			count = int32(n)
		}
	}

	client := pingv1connect.NewPingServiceClient(
		&http.Client{
			Transport: &http2.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
		host,
		connect.WithProtoJSON(),
	)

	fmt.Printf("streaming %d messages from %s ...\n\n", count, host)
	start := time.Now()

	stream, err := client.Count(context.Background(), connect.NewRequest(&pingv1.CountRequest{
		Count: count,
	}))
	if err != nil {
		log.Fatal(err)
	}

	for stream.Receive() {
		msg := stream.Msg()
		elapsed := time.Since(start).Truncate(time.Millisecond)
		fmt.Printf("[%s] #%d  proto=%s\n", elapsed, msg.Number, msg.Protocol)
	}

	if err := stream.Err(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\ndone — %d messages in %s\n", count, time.Since(start).Truncate(time.Millisecond))
}
