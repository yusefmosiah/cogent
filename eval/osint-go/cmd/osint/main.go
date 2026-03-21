package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/handler"
	"github.com/yusefmosiah/fase/eval/osint-go/internal/repository"
	"github.com/yusefmosiah/fase/eval/osint-go/internal/scanner"
	"github.com/yusefmosiah/fase/eval/osint-go/internal/service"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	timeout := flag.Duration("timeout", 4*time.Second, "per-scan timeout")
	ttl := flag.Duration("ttl", 2*time.Minute, "result cache ttl")
	flag.Parse()

	cache := repository.NewCache(*ttl)
	defer cache.Close()

	aggregator := service.NewAggregator(
		cache,
		*timeout,
		scanner.NewDNSScanner(),
		scanner.NewWhoisScanner(),
		scanner.NewHTTPHeaderScanner(),
	)
	h := handler.New(aggregator, cache)

	log.Printf("OSINT service listening on %s", *addr)
	if err := http.ListenAndServe(*addr, h.Routes()); err != nil {
		log.Fatal(err)
	}
}
