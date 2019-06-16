package secret_server_task_test

import (
	"flag"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sst "github.com/evsan/secret-server-task"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

const (
	secretText     = "secret"
	remainingViews = 100
	expiresDelta   = 100
	goroutines     = 10000
)

var db *sqlx.DB

func TestMain(m *testing.M) {
	dbUrl := flag.String("dbUrl", "", "db url for integration tests")
	flag.Parse()

	if !testing.Short() {
		// Connect to the Database for integration tests
		if *dbUrl != "" {
			db = sqlx.MustConnect("postgres", *dbUrl)
		}
	}

	// Run test
	i := m.Run()

	if !testing.Short() && db != nil {
		// If we were connected we need to close the connection
		err := db.Close()
		if err != nil {
			panic(err)
		}
	}

	os.Exit(i)
}

func TestSecret_IsAvailable(t *testing.T) {
	var testCases = map[string]struct {
		Expected      bool
		TTL           int
		ExpAfterViews int
	}{
		"TTL forever, ExpiresAfterViews > 0": {
			Expected:      true,
			TTL:           0,
			ExpAfterViews: remainingViews,
		},
		"TTL in future, ExpiresAfterViews > 0": {
			Expected:      true,
			TTL:           expiresDelta,
			ExpAfterViews: remainingViews,
		},
		"TTL in past, ExpiresAfterViews > 0": {
			Expected:      false,
			TTL:           -expiresDelta,
			ExpAfterViews: remainingViews,
		},
		"TTL forever, ExpiresAfterViews == 0": {
			Expected:      false,
			TTL:           0,
			ExpAfterViews: 0,
		},
		"TTL in future, ExpiresAfterViews == 0": {
			Expected:      false,
			TTL:           expiresDelta,
			ExpAfterViews: 0,
		},
		"TTL in past, ExpiresAfterViews == 0": {
			Expected:      false,
			TTL:           -expiresDelta,
			ExpAfterViews: 0,
		},
	}

	for name, tst := range testCases {
		t.Run(name, func(t *testing.T) {
			var expAt time.Time
			if tst.TTL != 0 {
				expAt = time.Now().Add(time.Duration(tst.TTL) * time.Minute)
			}
			s := sst.Secret{
				ExpiresAt:      expAt,
				RemainingViews: tst.ExpAfterViews,
			}
			if s.IsAvailable() != tst.Expected {
				t.Fail()
			}
		})
	}
}

func TestNewSecret(t *testing.T) {
	testCases := map[string]struct {
		SecretText        string
		ExpiresAfter      int
		ExpiresAfterViews int
		ExpError          error
	}{
		"correct": {
			ExpError:          nil,
			SecretText:        secretText,
			ExpiresAfter:      expiresDelta,
			ExpiresAfterViews: remainingViews,
		},
		"correct, forever TTL": {
			ExpError:          nil,
			SecretText:        secretText,
			ExpiresAfter:      0,
			ExpiresAfterViews: remainingViews,
		},
		"wrong secret text": {
			ExpError:          sst.ErrEmptySecret,
			ExpiresAfter:      expiresDelta,
			ExpiresAfterViews: remainingViews,
		},
		"wrong expires after": {
			ExpError:          sst.ErrInvalidExpireAfter,
			SecretText:        secretText,
			ExpiresAfter:      -expiresDelta,
			ExpiresAfterViews: remainingViews,
		},
		"wrong expires after views": {
			ExpError:          sst.ErrInvalidExpireAfterViews,
			SecretText:        secretText,
			ExpiresAfter:      expiresDelta,
			ExpiresAfterViews: 0,
		},
	}

	for name, tst := range testCases {
		t.Run(name, func(t *testing.T) {
			_, e := sst.NewSecret(tst.SecretText, tst.ExpiresAfterViews, tst.ExpiresAfter)
			if e != tst.ExpError {
				t.Fatalf("expected: %s, result: %s", tst.ExpError, e)
			}
		})
	}
}

func TestIntegrationMemStorage(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	storage := sst.NewMemStorage()

	t.Run("in-memory", integrationStorageTest(storage))

	if db != nil {
		storage = sst.NewPgStorage(db)
		t.Run("Postgres", integrationStorageTest(storage))
	}
}

func integrationStorageTest(storage sst.Storage) func(t *testing.T) {
	return func(t *testing.T) {
		secret, err := storage.Store(secretText, remainingViews, expiresDelta)
		if err != nil {
			t.Fatal("error is not expected: ", err)
		}

		var resultsAmount int32
		var wg sync.WaitGroup

		wg.Add(goroutines)

		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				v, e := storage.Get(secret.Hash)
				if e == nil && v.Hash == secret.Hash {
					atomic.AddInt32(&resultsAmount, 1)
				}
			}()
		}

		wg.Wait()

		if resultsAmount != remainingViews {
			t.Fatalf("expected: %d, result: %d", remainingViews, resultsAmount)
		}
	}
}
