package hostrate_test

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"time"

	"golang.org/x/time/rate"

	"github.com/richardwooding/hostrate"
)

// Example shows the convenience constructor: an http.Client that allows at most
// 2 requests per second to each host, bursting up to 5.
func Example() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello")
	}))
	defer srv.Close()

	client := hostrate.NewClient(2, 5)

	resp, err := client.Get(srv.URL)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))
	// Output: hello
}

// ExampleNew shows composing the Transport over a custom base transport, with a
// custom key and idle eviction to bound memory when reaching many hosts.
func ExampleNew() {
	base := &http.Transport{MaxConnsPerHost: 10}

	transport := hostrate.New(base, rate.Limit(2), 5,
		hostrate.WithKeyFunc(hostrate.KeyByHostPort),
		hostrate.WithIdleTimeout(10*time.Minute),
	)
	client := &http.Client{Transport: transport}

	_ = client // use client for outbound requests
	fmt.Println("configured")
	// Output: configured
}
