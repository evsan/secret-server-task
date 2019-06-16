package secret_server_task

import (
	"encoding/hex"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// Errors
var (
	ErrInvalidExpireAfter      = errors.New("invalid expireAfter, the value should be 0 or higher")
	ErrInvalidExpireAfterViews = errors.New("invalid expireAfterViews, the value should be positive")
	ErrEmptySecret             = errors.New("secret can't be empty")
	ErrSecretNotAvailable      = errors.New("secret is not available")
)

// Secret represents the secret entity
type Secret struct {
	Hash           string    `json:"hash" xml:"hash" db:"id"`
	SecretText     string    `json:"secretText" xml:"secretText" db:"secret_text"`
	CreatedAt      time.Time `json:"createdAt" xml:"createdAt" db:"created_at"`
	ExpiresAt      time.Time `json:"expiresAt" xml:"expiresAt"`
	RemainingViews int       `json:"remainingViews" xml:"remainingViews" db:"remaining_views"`
}

func (s *Secret) IsAvailable() bool {
	return (s.ExpiresAt.IsZero() || s.ExpiresAt.After(time.Now())) && s.RemainingViews > 0
}

// GenHashKey generates the hash key for the secret. Uses UUID for unique ids
func GenHashKey() string {
	id := uuid.New()
	return hex.EncodeToString(id[:])
}

// NewSecret creates the secret with generated Hash and validates input values
func NewSecret(secret string, expireAfterViews, expireAfter int) (Secret, error) {
	var result Secret
	result.Hash = GenHashKey()
	result.CreatedAt = time.Now()

	if secret == "" {
		return Secret{}, ErrEmptySecret
	}
	result.SecretText = secret

	if expireAfterViews < 1 {
		return Secret{}, ErrInvalidExpireAfterViews
	}
	result.RemainingViews = expireAfterViews

	if expireAfter < 0 {
		return Secret{}, ErrInvalidExpireAfter
	}

	if expireAfter > 0 {
		result.ExpiresAt = result.CreatedAt.Add(time.Duration(expireAfter) * time.Minute)
	}

	return result, nil
}

// Store is a repository/service interface for secrets.
type Storage interface {
	// Store creates the new record in the database with the given values.
	// Returns the created ID (Secret.Hash) and error if any
	Store(secret string, expireAfterViews, expireAfter int) (Secret, error)
	// Get checks the existence of a secret with the given key
	// and validates the expire conditions
	Get(key string) (Secret, error)
}

/*
 * In memory Storage implementation
 */

// memStorage implements Storage interface and uses in-memory map for storing the data
type memStorage struct {
	values sync.Map
}

type memSecret struct {
	mu sync.Mutex
	Secret
}

// NewMemStorage creates the memory based storage
func NewMemStorage() Storage {
	return &memStorage{}
}

// Store
func (st *memStorage) Store(secret string, expireAfterViews int, expireAfter int) (Secret, error) {
	var err error
	var mSecret memSecret

	mSecret.Secret, err = NewSecret(secret, expireAfterViews, expireAfter)
	if err != nil {
		return Secret{}, err
	}
	st.values.Store(mSecret.Hash, &mSecret)

	return mSecret.Secret, nil
}

// Get
func (st *memStorage) Get(key string) (Secret, error) {
	secret, ok := st.values.Load(key)
	if !ok {
		return Secret{}, ErrSecretNotAvailable
	}
	mSecret := secret.(*memSecret)

	// I've used check-lock-check pattern.
	// If the record is already not available there is no need to lock the mutex.
	// If it is available then we need to lock the mutex and check the availability again.
	// Then reduce the amount of available views
	if mSecret.IsAvailable() {
		mSecret.mu.Lock()
		defer mSecret.mu.Unlock()

		if mSecret.IsAvailable() {
			mSecret.RemainingViews--
			return mSecret.Secret, nil
		}
	}

	// Secret is expired, remove it from the memory
	st.values.Delete(key)

	return Secret{}, ErrSecretNotAvailable
}

/*
 * Storage implementation using PostgreSQL
 */

type pgSecret struct {
	Secret
	ExpiresAt pq.NullTime `db:"expires_at"`
}

func (p *pgSecret) ToSecret() Secret {
	if p.ExpiresAt.Valid {
		p.Secret.ExpiresAt = p.ExpiresAt.Time
	}
	return p.Secret
}

// pgStorage implements Storage interface and uses PostgreSQL.
type pgStorage struct {
	db *sqlx.DB
}

// NewPgStorage creates the PostgreSQL based storage
func NewPgStorage(db *sqlx.DB) Storage {
	return &pgStorage{db: db}
}

func (st *pgStorage) Store(secret string, expireAfterViews int, expireAfter int) (Secret, error) {

	var err error
	var pSecret pgSecret

	pSecret.Secret, err = NewSecret(secret, expireAfterViews, expireAfter)
	if err != nil {
		return Secret{}, err
	}

	pSecret.ExpiresAt.Valid = !pSecret.Secret.ExpiresAt.IsZero()

	q := "INSERT INTO secret(id, secret_text, created_at, expires_at, remaining_views) values(:id, :secret_text, :created_at, :expires_at, :remaining_views)"
	_, err = st.db.NamedExec(q, pSecret)

	if err != nil {
		return Secret{}, err
	}
	return pSecret.Secret, nil
}

func (st *pgStorage) Get(key string) (secret Secret, err error) {
	var tx *sqlx.Tx
	tx, err = st.db.Beginx()
	if err != nil {
		log.Println(err)
		return Secret{}, ErrSecretNotAvailable
	}
	defer func() {
		if err != nil && err != ErrSecretNotAvailable {
			log.Println(err)
			err = ErrSecretNotAvailable
			e := tx.Rollback()
			if e != nil {
				log.Println(e)
			}
			return
		}
		if e := tx.Commit(); e != nil {
			log.Println(e)
			err = ErrSecretNotAvailable
		}
	}()

	var pSecret pgSecret
	q := "SELECT id, secret_text, created_at, expires_at, remaining_views FROM secret WHERE id=$1 FOR UPDATE"
	err = tx.Get(&pSecret, q, key)
	if err != nil {
		return Secret{}, err
	}

	secret = pSecret.ToSecret()

	if secret.IsAvailable() {
		secret.RemainingViews--
		q = "UPDATE secret set remaining_views = remaining_views-1 WHERE id=$1"
		_, err = tx.Exec(q, key)
		if err != nil {
			return Secret{}, err
		}
		return secret, nil
	} else {
		q = "DELETE FROM secret WHERE id=$1"
		_, err = tx.Exec(q, key)
		if err != nil {
			return Secret{}, err
		}
		return Secret{}, ErrSecretNotAvailable
	}
}
