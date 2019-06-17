package main

import (
	"flag"

	sst "github.com/evsan/secret-server-task"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

func main() {
	dbUrl := flag.String("dbUrl", "", "postgres db url. If empty in-memory storage will be used")
	apiAddr := flag.String("apiAddr", ":8001", "http port for API")
	metricsAddr := flag.String("metricsAddr", ":9001", "http port for /metrics endpoint")
	debug := flag.Bool("debug", false, "enable debug mode")

	flag.Parse()

	var storage sst.Storage
	var db *sqlx.DB

	if *dbUrl != "" {
		db = sqlx.MustConnect("postgres", *dbUrl)
		storage = sst.NewPgStorage(db)
	} else {
		storage = sst.NewMemStorage()
	}

	app := App{
		Storage:     storage,
		ApiAddr:     *apiAddr,
		MetricsAddr: *metricsAddr,
		Debug:       *debug,
	}
	app.Run()
}
