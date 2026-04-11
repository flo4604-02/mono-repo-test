package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	host := "https://local-api-local.unkey.local"
	if h := os.Getenv("HOST"); h != "" {
		host = h
	}

	count := "10"
	if c := os.Getenv("COUNT"); c != "" {
		count = c
	}

	url := fmt.Sprintf("%s/stream?count=%s", host, count)
	fmt.Printf("SSE streaming from %s ...\n\n", url)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}

	start := time.Now()

	resp, err := client.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	fmt.Printf("connected — status %d, content-type: %s\n\n", resp.StatusCode, resp.Header.Get("Content-Type"))

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		elapsed := time.Since(start).Truncate(time.Millisecond)
		fmt.Printf("[%s] %s\n", elapsed, line)
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\ndone in %s\n", time.Since(start).Truncate(time.Millisecond))
}
