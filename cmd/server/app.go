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
	"github.com/urfave/negroni"
)

type App struct {
	Storage     sst.Storage
	ApiAddr     string
	MetricsAddr string
	Debug       bool
	Marshalers  map[string]Marshaler
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
	a.initMarchalers()

	apiRouter := mux.NewRouter()
	apiRouter.StrictSlash(true)

	apiRouter.HandleFunc("/secret/{hash}", a.getSecretHandler).Methods(http.MethodGet)
	apiRouter.HandleFunc("/secret", a.storeSecretHandler).Methods(http.MethodPost)

	go func() {
		err := http.ListenAndServe(a.MetricsAddr, http.HandlerFunc(a.metricsHandler))
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

func (a *App) metricsHandler(w http.ResponseWriter, r *http.Request) {
	_, err := w.Write([]byte("metrics hello!"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (a *App) getSecretHandler(w http.ResponseWriter, r *http.Request) {
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
