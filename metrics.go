package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	cacheChecks = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "upstream_api_cache_check",
		Help: "Number of hits/misses for the app's in-memory cache of upstream API results",
	}, []string{"type", "hit"})
)

func init() {
	go func() {
		//mux = http.NewServeMux()
		s := &http.Server{
			Addr:    ":2112",
			Handler: promhttp.Handler(),
		}
		//mux.Handle("/metrics", promhttp.Handler())
		s.ListenAndServe()
	}()
}
