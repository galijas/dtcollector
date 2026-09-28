// Command dtc-sample prints a synthetic, valid report (for development and
// for testing SwarmDialer's side of the contract), or uploads it.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"dtcollector/internal/report"
)

func main() {
	host := flag.Int("host", 0, fmt.Sprintf("synthetic host 0-%d", len(report.SampleHosts)-1))
	seed := flag.Uint64("seed", uint64(time.Now().UnixNano()), "random seed")
	url := flag.String("upload", "", "upload to this base URL instead of printing (e.g. https://dt.example.com)")
	key := flag.String("key", os.Getenv("DTC_UPLOAD_KEY"), "upload key (or DTC_UPLOAD_KEY)")
	flag.Parse()
	if *host < 0 || *host >= len(report.SampleHosts) {
		log.Fatalf("-host must be 0-%d", len(report.SampleHosts)-1)
	}
	r := report.Sample(report.SampleHosts[*host], *seed, time.Now())
	if *url == "" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(r)
		return
	}
	b, _ := json.Marshal(r)
	req, _ := http.NewRequest("POST", *url+"/api/v1/reports", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+*key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	fmt.Printf("%d %s", resp.StatusCode, out)
}
