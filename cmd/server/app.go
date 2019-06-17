package main

import (
	"encoding/json"
	"encoding/xml"
	"log"
	"net/http"
	"strconv"
	"strings"

	sst "github.com/evsan/secret-server-task"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/urfave/negroni"
)

type App struct {
	Storage     sst.Storage
	ApiAddr     string
	MetricsAddr string
	Debug       bool
	Marshalers  map[string]Marshaler
	Metrics     Metrics
}

type Metrics struct {
	secretGetCounter   prometheus.Counter
	secretPostCounter  prometheus.Counter
	secretGetDuration  prometheus.Summary
	secretPostDuration prometheus.Summary
}

// Accept Header
// I didn't find the library to parse Accept headers correctly.
// So I've decided to create my own parser as a quick fix for this task.
// My method ignores ;q=(q-factor weighting) and order of types
type Marshaler struct {
	MarshalFunc func(interface{}) ([]byte, error)
	ContentType string
}

func (a *App) Run() {
	a.initMetrics()
	a.initMarchalers()

	apiRouter := mux.NewRouter()
	apiRouter.StrictSlash(true)

	apiRouter.HandleFunc("/secret/{hash}", a.getSecretHandler).Methods(http.MethodGet)
	apiRouter.HandleFunc("/secret", a.storeSecretHandler).Methods(http.MethodPost)

	metricsRouter := mux.NewRouter()
	metricsRouter.StrictSlash(true)
	metricsRouter.Handle("/metrics", promhttp.Handler())

	go func() {
		err := http.ListenAndServe(a.MetricsAddr, metricsRouter)
		if err != nil {
			log.Println("metrics are not available")
		}
	}()
	// Standard middleware
	recovery := negroni.NewRecovery()
	recovery.PrintStack = a.Debug

	handler := negroni.New(recovery, negroni.NewLogger(), a.CorsMiddleware())

	// Serving static files if configured
	handler.UseHandler(apiRouter)

	log.Fatal(http.ListenAndServe(a.ApiAddr, handler))
}

func (a *App) CorsMiddleware() negroni.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept")
		if r.Method != http.MethodOptions {
			next(w, r)
		}
	}
}

func (a *App) getSecretHandler(w http.ResponseWriter, r *http.Request) {
	a.Metrics.secretGetCounter.Inc()
	timer := prometheus.NewTimer(a.Metrics.secretGetDuration)
	defer timer.ObserveDuration()

	vars := mux.Vars(r)
	key := vars["hash"]
	s, err := a.Storage.Get(key)
	if err != nil {
		http.Error(w, "Secret not found", http.StatusNotFound)
		return
	}
	a.dataResponse(s, w, r)
}

func (a *App) storeSecretHandler(w http.ResponseWriter, r *http.Request) {
	a.Metrics.secretPostCounter.Inc()
	timer := prometheus.NewTimer(a.Metrics.secretPostDuration)
	defer timer.ObserveDuration()

	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Invalid input", http.StatusMethodNotAllowed)
		return
	}

	secretText := r.FormValue("secret")
	expAfter, err := strconv.Atoi(r.FormValue("expireAfter"))
	if err != nil {
		http.Error(w, "Invalid input", http.StatusMethodNotAllowed)
		return
	}

	expAfterViews, err := strconv.Atoi(r.FormValue("expireAfterViews"))
	if err != nil {
		http.Error(w, "Invalid input", http.StatusMethodNotAllowed)
		return
	}

	secret, err := a.Storage.Store(secretText, expAfterViews, expAfter)
	if err != nil {
		http.Error(w, "Invalid input", http.StatusMethodNotAllowed)
		return
	}
	a.dataResponse(secret, w, r)
}

func (a *App) dataResponse(data interface{}, w http.ResponseWriter, r *http.Request) {
	m := a.getMarshaler(r.Header.Get("Accept"))
	if m.ContentType == "" {
		http.Error(w, "Accept header is invalid", http.StatusMethodNotAllowed)
		return
	}
	bytes, err := m.MarshalFunc(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-type", m.ContentType)
	_, err = w.Write(bytes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusMethodNotAllowed)
		return
	}
}

func (a *App) initMarchalers() {
	jsonMarshaler := Marshaler{
		MarshalFunc: json.Marshal,
		ContentType: "application/json",
	}
	xmlTextMarshaler := Marshaler{
		MarshalFunc: xml.Marshal,
		ContentType: "text/xml",
	}
	xmlAppMarshaler := Marshaler{
		MarshalFunc: xml.Marshal,
		ContentType: "application/xml",
	}
	a.Marshalers = map[string]Marshaler{
		"*/*":              jsonMarshaler,
		"application/json": jsonMarshaler,
		"application/*":    jsonMarshaler,
		"application/xml":  xmlAppMarshaler,
		"text/xml":         xmlTextMarshaler,
		"text/*":           xmlTextMarshaler,
	}
}

func (a *App) getMarshaler(acceptHeader string) Marshaler {
	for key, val := range a.Marshalers {
		if strings.Contains(acceptHeader, key) {
			return val
		}
	}
	return Marshaler{}
}

func (a *App) initMetrics() {
	a.Metrics.secretGetCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "secret_get_requests_total",
		Help: "The total number of GET /secret/{hash} requests",
	})

	a.Metrics.secretPostCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "secret_post_requests_total",
		Help: "The total number of POST /secret requests",
	})

	a.Metrics.secretPostDuration = promauto.NewSummary(prometheus.SummaryOpts{
		Name:       "secret_post_request_duration",
		Help:       "Histogram for the POST /secret response time",
		Objectives: map[float64]float64{0.5: 0.1, 0.9: 0.01, 0.99: 0.001},
	})

	a.Metrics.secretGetDuration = promauto.NewSummary(prometheus.SummaryOpts{
		Name:       "secret_get_request_duration",
		Help:       "Histogram for the GET /secret/{hash} response time",
		Objectives: map[float64]float64{0.5: 0.1, 0.9: 0.01, 0.99: 0.001},
	})
}
